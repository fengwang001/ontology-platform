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

## 单败淘汰赛对阵树（`bracket` 包）

`bracket` 包实现带轮空与退赛级联晋级的单败淘汰赛，全部 API 持有互斥锁，
可并发调用，等价于某一串行顺序。

### 种子折叠位置

- 选手为种子号 1..N（`New(n)`，N 必须在 2..64，否则返回 `ErrInvalidN`）。
- B 为不小于 N 的最小 2 的幂；折叠位置序列递归定义：
  `order(2)=[1,2]`，`order(2m)` 对 `order(m)` 中每个 s 依次写出
  `s, 2m+1−s`。例如 `order(8)=[1,8,4,5,2,7,3,6]`。
- 首轮第 i 场由序列第 `2i−1`、`2i` 个位置的种子对阵；种子号大于 N 者缺席，
  同场选手直接晋级，该场标记为 `Bye`（轮空场，`Report` 返回 `ErrByeMatch`）。
- 该排布保证种子 1 与种子 2 分别位于上下半区，只可能在决赛相遇。

### 轮空与晋级位置规则

- 共 `log2(B)` 轮；第 r 轮第 i 场的胜者进入第 r+1 轮第 `⌈i/2⌉` 场：
  i 为奇数进左位，偶数进右位（实现中即下一轮 `i/2` 号场次的左/右槽位）。
- 两个位置都有人的场次才算就绪；最后一轮胜者为冠军（`Champion()`）。
- `Bracket()` 按轮返回每场的 `Left/Right/Winner/Type`，类型为
  `Unresolved`（未决）、`Manual`（人工）、`Technical`（技术判定）、
  `Bye`（轮空）。轮空/晋级选手始终保留在其所在位置槽位中。

### 操作与拒绝原因（严格按给定顺序只报第一个）

- `Report(r,i,w)`：场次不存在 `ErrMatchNotFound` → 首轮轮空 `ErrByeMatch`
  → 已有结果 `ErrAlreadyPlayed` → 未就绪 `ErrNotReady`
  → w 非参赛者 `ErrNotContestant`。
- `Correct(r,i,w)`：不存在 → 轮空 → 无结果 `ErrNoResult`
  → 技术判定不可更正 `ErrTechnicalMatch` → w 非参赛者 → w 即现任胜者
  `ErrAlreadyWinner` → 下一轮对应场次已有结果 `ErrNextHasResult`。
  更正把胜者换为另一名参赛者、交换双方淘汰标记，并替换下一轮同一位置；
  决赛无下一轮，总是允许。
- `Withdraw(s)`：种子越界 `ErrSeedOutOfRange` → 已退赛 `ErrAlreadyOut`
  → 已输掉比赛 `ErrEliminated`（含技术判定落败）
  → 冠军已决出 `ErrChampionCrowned`。
- 任何被拒绝的操作都不改变场次、退赛标记与自动判定结果。

### 退赛级联判定

每次成功的 `Report`/`Correct`/`Withdraw` 后，反复扫描全部就绪且未决的场次：

- 仅一方已退赛：另一方技术晋级；
- 双方均已退赛：种子号较小者技术晋级；
- 技术判定结果记为 `Technical`，胜者继续填入下一轮，可能立即使新场次就绪，
  因此循环直到没有可判定的场次为止。

选手在对手尚未确定时退赛会留在原位；对手一旦通过登记/技术判定就绪，
级联立即让其自动晋级（可跨越多轮）。

### 本地验证

```bash
# 全部测试（含 2000 组随机对拍；若默认缓存目录只读可指定 GOCACHE）
GOCACHE=/tmp/gocache go test -v ./bracket/

# 竞态检测 + 全部测试
GOCACHE=/tmp/gocache go test -race ./...

# 静态检查
go vet ./...
gofmt -l .
```

测试内容：

- N=2/3/6/8/64 的首轮与轮空（N=6 验证种子 1、2 轮空及次轮对位）；
- 从首轮登记到冠军的完整流程；
- `Correct` 在下一轮未决时把新胜者放入同一位置、已决时被拒；决赛更正；
- 对手未定时退赛，就绪后自动晋级并跨轮级联；双方退赛种子号小者晋级；
- 技术判定不可更正、被拒绝操作不改变任何状态；
- N=2..64 逐场与按 `order(B)` 公式写成的朴素实现对照
  （`TestExhaustiveInitialBracket`）；
- 2000 组随机 `Report/Correct/Withdraw` 序列与独立参考实现逐步对拍
  （`TestRandomDifferential`，`-v` 日志打印每步输入、输出与判定依据）；
- 多 goroutine 并发压测与树不变量检查（`-race`）。
