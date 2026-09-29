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

## committer：两阶段原子提交器

`committer` 包实现变更流消费位点与外部副作用的两阶段原子提交，保证崩溃重启后不丢效果且始终可复现。

### 两阶段顺序

每条事件按连续递增序号投递，处理分两个阶段，**先写副作用，再进位点**：

1. **阶段一 `WriteEffect(seq, effect)`**：仅当 `seq == 已提交位点 + 1` 时，将副作用原子持久化（临时文件 + rename + fsync）。同一在途序号重复写入幂等。
2. **阶段二 `Commit(seq)`**：仅当 `seq == 已提交位点 + 1` 且该序号的副作用已持久化时，才推进位点。

### 位点连续性规则

- 序号必须为正且大于已提交位点，否则按**非法序号**（`ErrInvalidSeq`）拒绝。
- 写副作用时序号大于已提交位点加一，按**越序写入**（`ErrOutOfOrder`）拒绝。
- 提交时序号大于已提交位点加一，按**位点跳跃**（`ErrPositionJump`）拒绝。
- 提交时副作用尚未写入，按**缺效果提交**（`ErrMissingEffect`）拒绝。
- 任何拒绝都发生在状态变更之前：一次失败不改变副作用存储与位点。
- 已提交位点单调不减；对同一在途序号的并发重复写入，位点恰好推进一次。

### 崩溃恢复

崩溃重启后通过 `Open(path)` 重新打开同一状态文件，已持久化的副作用与位点全部保留。`Pending()` 返回"已写副作用但位点未提交"的序号，调用方对这些序号重复执行两阶段即可（重写幂等、提交恰好生效一次）。`Check()` 可随时自检位点连续性：1..position 的副作用齐全，且不存在超过 position+1 的越序副作用。

### 本地验证：重放操作序列核对结果

所有操作都是确定的纯函数式判定（仅依赖当前位点与副作用存储），因此可以用重放核对：

1. 运行被测流程，记录操作序列日志（`write seq effect` / `commit seq`）。
2. 在全新状态文件上按序重放同一操作序列（忽略每次操作的返回值）。
3. 对比最终位点与副作用存储；对同一序列重放两次结果必须完全一致。

`committer/committer_test.go` 中的 `TestReplayDeterministic` 演示了这一方法：构造含合法与非法操作的序列，在两个独立状态文件上重放并逐条核对最终位点、副作用存储与 `Pending()` 待处理序号。

```bash
# 运行两阶段提交器全部测试（含崩溃、跳跃、幂等、并发场景，日志打印操作/存储/位点/判定依据）
go test -race -v ./committer/
```
