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

## 版本条件写入与删除墓碑（`versioned` 包）

`versioned` 包对可能乱序到达的变更事件做按版本号（Last-Writer-Wins）的条件写入，并用删除墓碑挡住删除后迟到的旧写入，保证最终状态可复现、并与“墓碑永不清除”的朴素参照（`Reference`）在满足前提时一致。

### 数据模型

- `Event{Key, Version, Op, Value}`：一条版本化变更，`Op` 为 `OpWrite` 或 `OpDelete`。
- 存活行 `map[key]Row{Version, Value}`：查询 `Get` 只能读到存活行。
- 墓碑 `map[key]tombVersion`：删除标记，不是存活行。
- 全局水位 `watermark`：已接收（通过校验并被提交的）批次中**所有事件版本的最大值**，被忽略的事件也参与水位计算。

### 应用规则

1. 键的当前版本：有存活行取存活行版本；否则有墓碑取墓碑版本；都没有取 `0`。
2. 事件仅当 `Version > 当前版本` 时应用，否则忽略且 `ignored` 计数 +1（等版本也忽略）。
3. 应用写入：upsert 存活行并清除该键的墓碑。
4. 应用删除：移除存活行并写入墓碑；键原本不存在也照样建墓碑。
5. 每个批次按切片顺序逐条应用；批次末尾统一执行墓碑回收。

### 水位与墓碑保留期

- 构造参数 `retention` 为版本差保留窗口：当 `watermark - 墓碑版本 >= retention` 时清除该墓碑，清除后该键回到无状态（当前版本重新为 `0`）。
- 边界：差值**达到**保留参数（`>=`）即清除；`retention == 0` 时墓碑在当次批次末尾立即清除，因为删除事件本身已把水位推到其版本。
- 墓碑清除后，版本不高于墓碑版本的旧写入将不再被挡，会以“新写入”身份复活该键。因此与朴素参照一致的前提是：**乱序事件的迟到幅度小于保留窗口**（迟到事件在到达时其版本仍严格大于被回收墓碑的版本）。包内的有界乱序随机流测试验证了该前提；`TestReferenceDivergenceWhenPremiseBroken` 显式演示前提被破坏时两实现的可见差异。

### 整批拒绝（失败不留痕）

所有校验在修改任何状态之前完成；批次被拒后存活行、墓碑、水位、`applied/ignored` 计数均不变。拒绝原因互不相同，可用 `errors.Is` 区分（统一包装为 `*BatchError`，带事件下标，批次级错误下标为 `-1`）：

| 错误哨兵 | 类别 | 触发条件 |
| --- | --- | --- |
| `ErrInvalidRetention` | 非法参数 | `retention < 0` |
| `ErrInvalidBatchLimit` | 非法参数 | `maxBatchSize <= 0` |
| `ErrBatchTooLarge` | 条目数超限 | 批次条数大于上限 |
| `ErrNilEvent` | 非法事件 | 事件为 `nil` |
| `ErrEmptyKey` | 非法事件 | 键为空串 |
| `ErrInvalidVersion` | 非法事件 | 版本号非正（`<= 0`） |
| `ErrInvalidOp` | 非法事件 | 操作既不是写入也不是删除 |
| `ErrNilValue` | 非法事件 | 写入事件 `Value == nil`（空字节切片合法） |
| `ErrDuplicateKey` | 非法事件 | 同一批次内出现重复键（顺序语义有歧义） |

### 并发与自检

- `Commit` 持写锁；`Get` / `TombstoneVersion` / `Watermark` / `Counts` / `Snapshot` / `SelfCheck` 均持读锁，可在另一执行体提交期间并发调用（已用 `-race` 覆盖）。
- `SelfCheck` 基于读锁快照校验不变量：存活行与墓碑不共存、版本号均为正、水位不低于状态中的最大版本、所有存活墓碑的年龄都未达到保留期。

### 日志

`WithLogger` 注入实现 `Printf(format, args...)` 的日志器（如 `*log.Logger`）。每个步骤打印：事件输入（键/操作/版本/值）、当前版本及来源（存活行/墓碑/无状态）、判定依据（`=> APPLY/IGNORE` 及原因）、墓碑回收决策，以及批次末的水位、计数与排序后的存活行。

### 快速上手

```go
store, err := versioned.New(
    10,                  // 墓碑保留期（版本差）
    100,                 // 单批最大条目数
    versioned.WithLogger(log.Default()),
)
err = store.Commit([]*versioned.Event{
    {Key: "a", Version: 1, Op: versioned.OpWrite, Value: []byte("x")},
    {Key: "b", Version: 2, Op: versioned.OpDelete},
})
row, ok := store.Get("a")
```

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细输出（日志用例会打印逐步判定日志）
go test -race -v ./versioned

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式化与静态检查
gofmt -l .
go vet ./...
```
