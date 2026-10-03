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

## 创意连续淘汰分流器（`eliminator` 包）

`eliminator` 包实现带零点击护栏与中途加入的创意连续淘汰分流器，
所有操作并发安全，相同操作序列重放结果完全一致。

### 构造与配额

`New(n, b, G)`：初始创意数 `n`（2..64，编号 0..n−1）、基础配额
`b`（1..10^6）、护栏曝光数 `G`（1..10^9）。轮次 `r` 从 1 起，
每个活跃创意本轮配额：

```
q_r = b × 2^min(r−1, 20)
```

即每轮翻倍，第 21 轮起封顶在 `b×2^20`。每个创意维护累计曝光
`sT`、累计点击 `cT`、本轮曝光 `sR`（初值均为 0）与状态（活跃/已淘汰）。

### `Next()` 分流选择规则

在本轮 `sR < q_r` 的活跃创意中，取 `sR` 最小者；`sR` 相同取编号最小者。
选中后其 `sT` 与 `sR` 各加一，然后**在同一次 Next 内、按固定顺序**处理：

1. **护栏（先）**：被选创意 `cT == 0` 且 `sT >= G` 且活跃创意数不少于 2 时，
   立即以原因 `"guard"` 淘汰；淘汰后只剩一个活跃创意则直接结束，该创意胜出
   （不再走收轮）。
2. **收轮（后）**：若所有活跃创意 `sR == q_r`，本轮结束：
   - 活跃创意按累计点击率 `cT/sT` 降序排序。比较使用整数交叉相乘
     `cT_i×sT_j` 对 `cT_j×sT_i`，杜绝浮点误差；点击率完全相等时编号小者在前。
   - 保留前 `⌈活跃数/2⌉` 个，其余以原因 `"round"` 淘汰。
   - 只剩一个则该创意胜出并结束；否则 `r += 1`，所有存活创意 `sR` 清零。

护栏先于收轮，因此当护栏淘汰的恰好是“最后一个未满配额”的创意时，
其余活跃者都已打满配额，同一次 Next 立即触发收轮。护栏淘汰与收轮是
Next 不可分割的一部分，任何并发观察者都看不到中间状态。

### `Click(arm)`

对任何已登记创意（含已淘汰者、结束之后），仅当 `cT < sT` 时成功并令
`cT += 1`。已淘汰创意的迟到点击只记账，不影响任何淘汰结果。点击可以
在曝光之后任意时刻到达：在某次收轮之后到达的点击不再影响该次排名，
这也是结果可精确复现的一部分（重放相同的交错顺序得到相同结果）。

### `Join(id)`

`id` 为 0..1000 中尚未登记过的编号，登记总数最多 64（按“曾经登记”计数，
淘汰不释放名额）。新创意以活跃状态加入**当前轮**：`sT=cT=sR=0`，
需要补满本轮配额，因此其 `sR` 最小会被 `Next` 立即优先分流。结束后不能 Join。

### 拒绝原因与优先级

被拒绝的操作不改变任何状态。按以下顺序只报第一个：

| 常量 | 含义 | 触发操作 |
| --- | --- | --- |
| `invalid_arguments` | 参数非法（构造越界、Join 编号越界） | `New`、`Join` |
| `already_finished` | 已结束 | `Next`、`Join` |
| `arm_not_found` | 创意不存在（编号未登记） | `Click` |
| `arm_already_exists` | 创意已存在（含已淘汰者） | `Join` |
| `capacity_full` | 登记总数已为 64 | `Join` |
| `click_without_exposure` | 点击无对应曝光（`cT >= sT`） | `Click` |

注意优先级细节：结束后 `Click` 未登记编号仍报 `arm_not_found`；
满员时对已登记编号（含已淘汰者）报 `arm_already_exists` 而非 `capacity_full`；
`Join` 的编号越界检查先于“已结束”。

### API 速览

```go
e, _ := eliminator.New(4, 2, 3)
r, _ := e.Next()        // r.Arm 分流编号；r.Eliminated 本次淘汰事件；
                        // r.RoundClosed 是否收轮；r.Winner（-1 表示未结束）
err := e.Click(arm)     // *eliminator.RejectError，err.Reason 区分原因
err = e.Join(id)
s := e.Snapshot()       // 轮次、结束标志、胜者、成功 Next 总数、各创意状态、淘汰事件流
```

不变量：任意时刻 `cT <= sT`；活跃创意 `sR <= q_r`；所有创意累计曝光之和
恰等于成功的 `Next` 次数；已淘汰创意不会复活；结束时恰有一个胜出者。

### 本地验证

```bash
# 全量测试（含 2000 组随机操作序列与朴素模拟的逐步差分对照）
go test ./eliminator/ -v

# 并发原子性竞态检测
go test -race ./eliminator/

# 只跑 2000 组差分对照（日志打印每组输入、输出与判定依据）
go test ./eliminator/ -run TestRandomDifferential -v

go vet ./...
gofmt -l .
```

`TestRandomDifferential` 用固定随机种子生成 2000 组 `Next/Click/Join`
随机序列，驱动两个独立实现（真实实现与按规则逐行写成的朴素模拟器），
每步比较拒绝原因、Next 返回（分流编号、淘汰事件、收轮标志、胜者）与
完整状态（`sT/cT/sR`、淘汰原因、轮次、事件流），失败时打印输入序列、
朴素侧判定日志与实现侧快照以便定位。
