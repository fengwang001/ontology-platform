# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## stagedcommit：带两阶段提交日志的暂存提交器

包 `stagedcommit`（[stagedcommit/committer.go](stagedcommit/committer.go)）把键值变更先放入
对读不可见的暂存区，再原子提交为可见视图。

### 提交协议（两阶段）

1. **暂存**：`Put` 写入/覆盖暂存变更；`Delete` 仅当键当前在可见视图存在且尚未在
   暂存区被改动时允许。暂存内容对 `Snapshot` 读不可见。
2. **第一阶段（准备）**：`Commit` 把暂存变更写入提交日志，条目记为 `prepared`，
   并消耗一个从 1 递增的提交号。
3. **第二阶段（定稿）**：把变更应用到可见视图，日志条目标记为 `finalized`，
   暂存区清空。

崩溃恰好发生在两步之间时：日志留下一条 `prepared` 未定稿条目，视图不变
（半成品提交零效果），暂存区照常清空，提交号照常消耗。可用 `ArmCrash` 武装
一次模拟崩溃复现该场景。

### 恢复规则

- `Recover` 把最新一条 `prepared` 未定稿条目判定为 `aborted`（视为从未发生）
  并返回其提交号；没有待判定条目返回 0，连续调用第二次必返回 0。
- `Rollback` 清空暂存区，永远成功且无痕迹（不写日志、不消耗提交号）。

### 错误判定

三类互不相同的错误，均可用 `errors.Is` 判定，失败不改变暂存区、视图、日志与
提交号计数，被拒后仍可继续正常使用：

- `ErrEmptyKey`：空键写入或删除。
- `ErrEmptyCommit`：暂存区为空时提交。
- `ErrDeleteNotAllowed`：删除目标不在可见视图，或已在暂存区被改动。

### 并发保证

所有方法可并发调用。`Snapshot` 返回的视图是不可变副本，并发只读同一实例得到
逐字段相同的视图，且任一读到的视图必然对应某个完整提交边界（`View.Commit`
标识该边界的提交号）。

### 本地验证

```bash
# 全部测试（含竞态检测与详细日志，日志中打印输入、结果与判定依据）
go test -race -v ./stagedcommit/

# 指定场景
go test -race -v -run TestCrashBetweenPhases ./stagedcommit/
```

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
