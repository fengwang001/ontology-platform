# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多视图全局一致读（ontology 包）

`SnapshotStore` 在多个视图进度不一致、且各自只保留有限历史版本的情况下，
提供全局一致、可复现的快照读取。

### 进度与版本保留

- 每个视图维护一个**进度时间戳**和一串按时间戳**严格递增**的版本。
- `Apply(view, ts, value)` 写入新版本并推进进度；`Heartbeat(view, ts)` 只推进进度。
  两者都要求 `ts` 为正且严格大于当前进度，否则整体拒绝。
- 每个视图只保留最近 `Retention` 个版本，更旧的版本被永久淘汰、不可再读。
- 视图在时间点 `ts` 的值取**时间戳不超过 `ts` 的最大版本**。

### 读取规则

`Read(ts)` 依次判定，全部通过才返回各视图在 `ts` 的值：

1. `ts` 必须为正，且不早于上一次成功读取的时间点（连续读取的时间点单调不减）；
2. `ts` 不得超过**各视图进度的最小值**（保证快照对应所有视图都已应用的时间点）；
3. 每个视图**最旧保留版本**不得晚于 `ts`（保证该点仍可复现）。

同一时间点只要仍可读，多次读取结果逐视图不变；读取可与应用/心跳并发进行。

### 错误类别

所有被拒绝的调用都**不改变进度、版本或保留情况**，失败不留痕。错误可用
`errors.Is` 区分：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidArgument` | 非法参数：未知视图、非正时间戳、空值、非法配置 |
| `ErrNonMonotonicTimestamp` | 时间戳不前进：应用/心跳未严格递增，或读取早于上次成功读取 |
| `ErrNotReady` | 尚未准备好：读取时间点超过各视图进度的最小值 |
| `ErrTooOld` | 太旧：存在视图最旧保留版本晚于读取时间点（版本已被淘汰） |

### 本地验证

```bash
# 全部测试（含朴素参照一致性、并发交叉校验）
go test ./ontology

# 竞态检测 + 每步输入/时间点/判定依据日志
go test -race -v ./ontology
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
