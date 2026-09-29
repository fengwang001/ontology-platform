# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 并行回放调度器（`scheduler` 包）

`scheduler` 包接收按提交顺序到达的事务（读键集合 + 写键→值映射），
基于**写集依赖**计算依赖深度、分批调度轮次，并并发回放得到最终状态。

### 依赖与深度

- 依赖仅由**写集相交**判定：事务 `j` 依赖所有序号更小、且与其写集有
  至少一个共同键的事务。读集仅作审计记录，不参与依赖判定，也不读取任何值。
- 同一事务写集内重复出现的键只算一个（Go map 天然去重）。
- 无依赖的事务深度为 `1`；有依赖的事务深度为
  `1 + max(所有依赖事务的深度)`。

### 轮次调度规则

- 按事务序号从小到大，逐轮挑选依赖已全部在前序轮次完成的事务。
- 每轮最多放入 `maxParallel` 个事务；同轮事务的写集两两不相交
  （因此不会互为依赖，可安全并发）。
- 未就绪或受同轮容量/冲突限制的事务顺延到下一轮。
- 回放时同一轮的事务在独立 goroutine 中真正并发执行，轮间等待；
  因为冲突写严格按提交顺序发生在不同轮次，最终状态与按提交顺序
  串行执行**逐键完全一致**。
- `Replay` 内置自检：用朴素串行参照逐事务复算最终状态，并校验轮次
  依赖关系、并行度上限与序号覆盖，结果在 `ReplayResult.SelfCheckOK`。

### 边界条件与错误类别

非法输入被**整体拒绝**（整批不生效），拒绝原因互不相同，
可通过 `scheduler.RejectReasonOf(err)` 获取：

| 原因 | 触发条件 |
| --- | --- |
| `nil_transactions` | `Add(nil)` |
| `empty_transactions` | 空事务批次 |
| `sequence_gap` | 序号不从 1 开始、不连续或不按 +1 递增（含跨批次续接） |
| `empty_write_set` | 事务写集为空 |
| `empty_write_key` | 写集包含空字符串键 |
| `too_many_transactions` | 接受后事务总数超过 `scheduler.MaxTransactions`（100000） |
| `invalid_parallelism` | `Schedule` / `Replay` 的并行度上限小于 1 |

任何一次拒绝都不会改变已接受事务、深度、调度结果与回放状态
（先完整校验，通过后才在锁内提交）。

### 日志

`New(w io.Writer)` 传入日志写入目标（`nil` 丢弃日志）。每个事务
接收时打印序号、读集、去重排序后的写集、深度及深度判定依据
（产生该深度的依赖序号列表）；调度时打印序号、轮次、并行度上限
与该轮大小；回放后打印自检结论。

### 快速上手

```go
s := scheduler.New(os.Stdout)
err := s.Add([]scheduler.Transaction{
    {Seq: 1, Reads: []string{"x"}, Writes: map[string]string{"a": "1"}},
    {Seq: 2, Writes: map[string]string{"a": "2", "b": "2"}},
})
depths := s.Depths()              // map[1:1 2:2]
rounds, _ := s.Schedule(4)        // 2 轮：[1] [2]
result, _ := s.Replay(4)          // State: a=2,b=2; SelfCheckOK: true
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
