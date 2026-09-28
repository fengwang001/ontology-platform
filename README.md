# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 前 N 名增量视图（`ontology` 包）

`ontology.TopNTracker` 在**撤回式变更流**（retraction stream）上增量维护排名前 N 的行：
变更只有 `Add`（新增一行）与 `Retract`（携带与新增时一致的分数撤回一行）两种。

### 排序规则

- 分数**降序**；分数相同时按键的**字典序升序**（`int64` 分数，字符串键）。
- 键在存活集合中唯一，比较为严格全序，排序结果确定、与插入顺序无关。

### 补位规则

- tracker 始终保留**全部存活行（含榜外）**，而不只是前 N 行。
- 榜内行被撤回时，由榜外按同一排序规则排最靠前的行补位进入前 N。
- 撤回榜外行不影响榜单；榜外无人可补时（存活行不足 N）榜单相应缩短。

### 输出规则（每条被接受的变更）

比较变更前后的前 N 名**集合**：

1. **先输出离开**（`Left`）：变更前在榜、变更后不在榜，按变更前名次升序；
2. **再输出进入**（`Entered`）：变更后在榜、变更前不在榜，按变更后名次升序；
3. 留存行的名次变动不单独输出，下游按同一排序规则重算名次即可
   （见 `TestReplayLogReconstructsTopN`：按序应用日志必能重建正确的前 N 名）。

被接受的变更同时追加到 `ChangeLog()`；被拒绝的变更**不产生日志、不改变任何状态**。

### 拒绝原因（可区分）

| `RejectReason` | 哨兵错误 | 触发条件 |
| --- | --- | --- |
| `RejectInvalidArgument` | `ErrInvalidArgument` | 构造参数非法（`n<=0`、上限为负）；变更种类未知；键为空 |
| `RejectDuplicateKey` | `ErrDuplicateKey` | 新增一个已经存活的键 |
| `RejectKeyNotFound` | `ErrKeyNotFound` | 撤回一个当前不存在的键 |
| `RejectScoreMismatch` | `ErrScoreMismatch` | 撤回携带的分数与该行存活分数不一致 |
| `RejectTooManyLiveRows` | `ErrTooManyLiveRows` | 新增会使存活行数超过 `maxLiveRows`（0 表示不限） |

通过 `ChangeResult.Reason` 取原因，或用 `errors.Is(ontology.ReasonError(r), ...)` 映射为错误。

### 并发与确定性

- `Apply` / `Snapshot` / `TopN` / `LiveCount` / `ChangeLog` 均内部加锁，可并发调用；
  返回切片都是独立副本，快照**逐字段一致**，不会被后续变更撕裂。
- 同一输入序列反复计算得到**完全相同**的输出（见 `TestDeterminism`）。

### 示例

```go
tr, _ := ontology.NewTopNTracker(3, 0) // 前 3 名，不限制存活行数
res := tr.Apply(ontology.Change{
    Kind: ontology.KindAdd,
    Row:  ontology.Row{Key: "a", Score: 100},
})
// res.Accepted / res.Left / res.Entered / res.Top
snap := tr.Snapshot() // snap.Top、snap.All（含榜外）、snap.LiveCount
```

可运行的端到端演示（覆盖并列、补位与各类拒绝，逐条打印输入、判定依据与输出）：

```bash
go run ./cmd/topndemo
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server

# 前 N 名组件演示
go run ./cmd/topndemo

# 测试（建议带竞态检测与详细输出，日志含每条输入、输出条目与判定依据）
go test -race -v ./ontology/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...
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
