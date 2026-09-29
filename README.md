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

## 跨阈值报警流（`alert` 包）

按键维护累计值（`int64`，从 0 开始，可增可减），跨越阈值时发出**边沿触发**的报警/解除事件，带迟滞带，不重复、不遗漏、可复现。

### 精确边界定义

设阈值 `T = Threshold`，迟滞带 `H = Hysteresis`，解除线 `L = T - H`：

- **报警（alarm）**：仅当键处于**关闭态**且新累计值 `v >= T` 时触发，随后转入开启态。`v == T` 恰好等于阈值**算触发**。
- **解除（clear）**：仅当键处于**开启态**且新累计值 `v < L` 时触发，随后转入关闭态。`v == L` 恰好等于解除线**仍保持开启**，不发事件。
- **迟滞带**：`v ∈ [L, T)` 为迟滞区。开启态下值在迟滞区或阈值上方抖动均不重复报警；关闭态下值低于 `T` 不发任何事件。
- 每次增减只在跨越边沿时产生至多一个事件；事件带全局单调递增 `Seq`，保证顺序可核对。

### 参数校验与原子性

以下非法输入被整体拒绝，且一次失败不改变任何键的值、状态与事件列表，原因可用 `errors.Is` 区分：

- `ErrThresholdNonPositive`：阈值非正
- `ErrHysteresisNonPositive`：迟滞带非正
- `ErrHysteresisNotBelowThld`：迟滞带不小于阈值
- `ErrEmptyKey`：空键
- `ErrOverflow`：增减量使累计值溢出 `int64`

### 并发语义

`Monitor` 的所有方法（`Add`/`Value`/`State`/`Events`/`EventsFor`）均可并发调用。内部以互斥锁串行化：同一键的并发增减互不冲突，并发读到的结果与某一串行参照一致。

### 本地验证：逐步重放核对事件序列

`alert.Replay(cfg, steps)` 在全新报警流上按序重放 `[]alert.Step{{Key, Delta}, ...}`，返回完整事件序列；任一步非法即整体失败。核对方法：

```bash
# 运行含重放比对的测试（TestReplayMatchesLiveRun 将实时运行与 Replay 结果逐一比对）
go test -v -run TestReplay ./alert/

# 带竞态检测的全量验证
go test -race -v ./alert/
```

单测日志会打印每一步的输入（key、delta）、累计值、事件类型与判定依据（如 `state=closed && value(100) >= threshold(100) => alarm`），可逐步对照预期事件序列。
