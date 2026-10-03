# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## redo：重做日志并行回放器

`redo` 包实现带小事务（MTR）原子性与文件操作屏障的重做日志并行回放器。
构造参数为回放线程数 `W`（1..64）与批次记录上限 `M`（1..1e6）：

```go
r, err := redo.NewReplayer(w, m)
err = r.LoadPage(space, page, value, lsn) // 仅回放开始前
err = r.Feed(rec)                          // PageRecord / FileRecord / EndRecord
err = r.Finish()
log := r.ApplyLog()                        // 按实际处理次序的 (lsn, 结果)
stats := r.Stats()
```

### 小事务的暂存与入队时机

- `Page` 记录先进入当前未结束小事务的**暂存区**，不入队、不计 `pending`。
- `End` 到达时，该小事务的全部暂存记录一次性进入分发队列：
  每条记录进入线程 `w=(space*7+page) mod W` 的队列，队列内保持 LSN 升序，
  `pending` 为各队列记录总数。这保证同一小事务的页记录要么全部入队、要么全部不入队（原子性）。
- `Finish` 时仍未 `End` 的小事务，其暂存记录被丢弃并计入「被丢弃数」。

### 批次触发与文件操作屏障

- `End` 使 `pending >= M` 时立即做一个批次（一个 `End` 可一次越过 `M`，只算一个批次）。
- `File` 记录到达时：若 `pending > 0` 先做一个批次（屏障），再立即执行该文件操作；
  `pending == 0` 时不算批次。`Create` 对已存在的表空间无效果，否则新建空表空间；
  `Delete` 对不存在的表空间无效果，否则删除该空间及其全部页状态。
- `Finish` 先做一个批次（仅 `pending > 0` 时计批次），再丢弃未 `End` 的暂存记录。
- 批次按线程号 `0..W-1` 依次处理各队列，队列内按 LSN 升序逐条处理；
  批次后队列清空、`pending` 归零、批次数加一。

### 应用 / 跳过 / 丢弃三种判定

批次内对每条页记录依次判定：

1. **空间缺失**：该记录的表空间不存在，丢弃（计入 `Missing`）。
2. **已应用**：`lsn <= pageLSN`（恰等也跳过），跳过（计入 `Skipped`）。
3. **应用**：否则 `value += delta`、`pageLSN = lsn`（计入 `Applied`）。

`ApplyLog()` 按实际处理次序返回 `(lsn, 结果)`。最终页状态与
「严格按 LSN 升序逐条处理全部已 End 的小事务记录」的顺序回放相同，与 `W`、`M` 无关；
相同输入重放得到完全相同的批次与 `ApplyLog`。

### 拒绝原因

按以下顺序只报第一个（`(*redo.Error).Code` 可区分）：
参数非法（`ErrInvalidArgs`）→ 回放已结束（`ErrFinished`）→ 回放已开始
（`ErrStarted`，仅 Load）→ LSN 不够大（`ErrLSN`）→ 小事务错误（`ErrMTR`）。
被拒绝的记录不改变任何状态，也不推进已接受的最大 LSN 与最大 mtr 号。

### 本地验证

```bash
# 全部单测（含规格示例、批次触发、屏障、生命周期、拒绝原因等）
go test ./redo -v

# 2000 组随机日志（含随机截断）对照朴素顺序回放，日志打印输入、输出与判定依据
go test ./redo -run TestRandomLogsAgainstNaive -v

# 竞态检测
go test -race ./redo
```
