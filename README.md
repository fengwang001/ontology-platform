# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 前 N 名增量视图（`topn` 包）

`topn.View` 在**撤回式变更流**（行的新增 `OpAdd` / 撤回 `OpRetract`）上增量维护排名前 N 的视图，并输出榜单的进入/离开日志。代码位于 [`topn/`](./topn)。

### 排序规则

- 分数**降序**；分数相同时按键的**字典序升序**。
- 排序在全部**存活行**（含榜内与榜外）上进行，取前 N 名。
- 榜外行同样保留：榜内行被撤回时，由榜外排序最靠前的行补位。

### 输出规则（`Apply` 的返回与累积日志）

每条变更成功处理后，比较处理前后的前 N 名集合：

1. **先输出离开（`KindLeave`）条目，再输出进入（`KindEnter`）条目**；
2. 离开条目按旧名次顺序排列，`Rank` 为该行离开前的名次（从 1 开始）；
3. 进入条目按新名次顺序排列，`Rank` 为该行进入后的名次；
4. 仅榜外变动（如撤回榜外行、榜外新增）不产生条目。

下游按顺序应用日志（先删后增）即可始终重建出正确的前 N 名；`View.Log()` 返回截至目前全部条目的副本。

### 拒绝规则

以下输入会被拒绝，错误为 `*topn.RejectError`，`Reason` 字段可区分原因；**被拒绝的输入不改变存活行、前 N 名或已产生日志**：

| Reason | 触发条件 |
| --- | --- |
| `ReasonInvalidArgument` | `n <= 0`、`maxLive < 0`、`maxLive < n`、空键、未知操作类型 |
| `ReasonDuplicateKey` | 新增一个已经存活的键 |
| `ReasonRetractMissing` | 撤回一个当前不存在的键 |
| `ReasonScoreMismatch` | 撤回时给定分数与该键存活行的分数不符 |
| `ReasonLiveLimitExceeded` | 新增会使存活行数超过 `maxLive`（0 表示不限） |

### 并发与确定性

- `Apply` 互斥串行；`Snapshot` / `LiveCount` / `Log` 均在读锁下返回**副本**，读者拿到的是逐字段一致的快照，可并发读取。
- 纯函数式排序，无随机来源：同一输入序列反复计算得到完全相同的输出。

### 用法示例

```go
v, _ := topn.New(3, 0) // 前 3 名，存活行数不限
entries, err := v.Apply(topn.Change{Op: topn.OpAdd, Key: "alice", Score: 100})
if err != nil {
    var reject *topn.RejectError
    if errors.As(err, &reject) {
        // reject.Reason 可区分拒绝原因
    }
}
fmt.Println(entries)        // 本次变更的离开/进入条目（先离开后进入）
fmt.Println(v.Snapshot())   // 当前前 N 名，按名次排序
fmt.Println(v.Log())        // 累积日志，下游可顺序重放
```

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

# 带竞态检测与详细输出（topn 包的并发测试建议始终带 -race）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology
go test -race -run TestConcurrentRead ./topn

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`topn` 包的测试用例通过 `t.Logf` 打印每条**输入**（操作/键/分数）、**输出条目**（离开/进入、名次）与**判定依据**（排序规则、拒绝原因），用 `-v` 运行即可查看：

```bash
go test -race -v ./topn
```

覆盖场景：分数并列时的字典序、撤回后的补位、先离开后进入的输出顺序、各类非法输入及拒绝无副作用、同序列重放的确定性、并发读取的一致性。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
