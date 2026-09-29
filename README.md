# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 写集依赖并行回放调度器

`ontology/scheduler` 接收按提交顺序到达的事务（读键/写键集合），计算
依赖深度、分批调度轮次，并支持真正并发的确定性回放。

### 依赖与深度定义

- 事务序号必须连续且从 1 递增；同一事务写集内重复出现的键只算一个。
- 两个事务存在依赖，当且仅当它们的**写集相交**；读集仅作审计记录，
  不参与依赖判定，回放时也不读取任何值。
- 对新事务的每个写键，只依赖该键的**最近一个写入者**（提交序前驱）。
- 无依赖的事务深度为 1；有依赖时深度 = `max(所有依赖的深度) + 1`。

### 轮次调度规则

给定并行度上限 `maxParallel`（正整数）：

- 按序号从小到大逐轮贪心挑选：依赖已全部在**更早轮次**完成的事务进入本轮，
  每轮至多 `maxParallel` 个。
- 同轮事务的写集两两不相交，因此同轮事务不会互为依赖，可真正并发执行。
- 轮次与深度不是同一概念：深度是结构属性，轮次还受并行度上限影响。
- 轮次之间有屏障：一轮全部结束后下一轮才开始。

### 回放语义

- `Replay` 用同一套轮次规则执行；同轮事务在不同 goroutine 上并发调用执行体，
  结果按轮次、轮内按序号合并；因同轮写集不相交，最终状态与按提交顺序
  串行执行逐键一致。
- 默认执行体：`WriteValues` 给出的键写入指定值，其余键写入确定性派生值
  `tx<序号>:<键>`，保证可复现。也可传入自定义 `Executor`；执行体必须只写
  事务声明的写键，否则回放整体失败。
- 深度、轮次、计划、回放、自检、审计日志均为只读查询，可被多个执行体
  并发调用；同一事务序列反复调用结果逐字段一致。

### 边界条件与错误类别

所有错误均为可区分的哨兵错误，用 `errors.Is` 判定；非法输入被**整体拒绝**，
任何一次拒绝都不改变已接受事务、深度、调度结果或回放状态（失败不留痕迹）。

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | 序号非正、读集含空键、写值映射到空键或非写键等 |
| `ErrSeqNotConsecutive` | 序号不是从 1 开始严格连续递增（跳号/重复） |
| `ErrWriteSetEmpty` | 写集为空（去重后无键） |
| `ErrWriteSetEmptyKey` | 写集包含空字符串键 |
| `ErrTxLimitExceeded` | 已接受事务数达到 `MaxTransactions`（100000）后再添加 |
| `ErrTxNotFound` | 查询不存在的事务序号 |
| `ErrInvalidParallel` | 并行度上限不是正整数 |

`SelfCheck` 校验序号连续、写集去重排序、依赖与深度一致、最近写入者映射一致。
`DumpAuditLog` 打印每个事务的输入（读/写键）、深度、轮次、依赖与判定依据。

### 本地验证

```bash
# 演示：审计日志、轮次、回放最终状态
go run ./cmd/replay-demo

# 单元测试（深度/回放与朴素参照逐事务对比、并发用 -race 验证）
go test -race -v ./ontology/scheduler

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
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
