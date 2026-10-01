# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 瑞士制轮次配对与积分登记器（`swiss` 包）

`swiss.Tournament` 按确定的瑞士制规则完成登记、逐轮配对、结果登记与积分榜输出，
相同操作序列重放得到完全相同的对阵、轮空与榜单。

### 基本用法

```go
tr, _ := swiss.New(5)            // 最大轮数 R，R 须在 1..20，否则构造失败
tr.Register("Alice")             // 种子号从 1 起，按登记先后分配
tr.Register("Bob")
pairs, bye, err := tr.Pair()     // 开始新一轮：pairs 为对阵，bye 为轮空者（偶数人时为 0）
tr.Report(1, 2, 2)               // 从 1 的视角登记：2 胜、1 平、0 负；2 得 2-r
rows := tr.Standings()           // []Row{Seed, Score, Buch}
```

### 轮空人选

- 选手数为奇数时，先在**尚未轮空过**的选手中选轮空者：积分最低者优先，
  积分相同取**种子号最大**者；无人可轮空则本轮整体失败（`ErrNoPairing`）。
- 轮空在开轮时即记 2 分（视同一场胜利），但不产生对手，也不计入对手分。
- 每名选手至多轮空一次。

### 配对步骤

1. 除轮空者外的全体选手按 **（积分降序, 种子号升序）** 排成序列。
2. 反复取序列中第一个尚未配对的选手 `x`，在其**之后**的选手中取第一个
   尚未配对且与 `x` 从未交手的 `y` 成对，按 `(x, y)` 记录。
3. **不回溯、不做上下半区对折**：同分组内相邻选手若已交手，就继续向后找
   第一个未交手者。某位 `x` 找不到合法对手时本轮整体失败
   （即使此时存在别的完美匹配，也不重新调整已确定的对子）。
- 任意两名选手至多交手一次；失败的 `Pair` 不产生轮空记录，也不改变任何状态。

### 积分与对手分口径

- 胜 2、平 1、负 0；轮空 2 分（无对手）。
- `Standings()` 返回 `(种子号, 积分, 对手分)`，按 **（积分降序, 对手分降序,
  种子号升序）** 排序。
- 对手分（Buchholz）= 该选手所有**真实对手**的**当前积分**之和；轮空不计。
  因此后续轮次中对手得分增长会抬高该选手的对手分。
- 不变量：全体积分之和 = 2 × 已登记盘数 + 2 × 轮空次数。
- 本轮全部盘登记完毕本轮才结束；同一轮内各盘的登记先后不影响榜单。

### 拒绝顺序（只报第一个错误）

- `Register`：已开始（首次 `Pair` 成功后）→ 名称为空 → 名称重复。
- `Pair`：本轮还有未登记的盘 → 已进行 R 轮 → 选手少于 2 人 →
  找不到合法配对（含无可轮空者）。
- `Report`：当前没有进行中的轮 → r 不是 0/1/2 →
  a、b 不是本轮的一盘（含 a==b、未知种子、轮空者）→ 该盘已登记。
- 被拒绝的操作不改变选手、轮次、轮空记录与已登记结果。

并发：所有方法由同一把互斥锁串行化，并发调用等价于某个串行顺序。

### 本地验证

```bash
# 全部测试
go test ./swiss/

# 竞态检测 + 重复执行
go test -race -count=3 ./swiss/

# 2000 组随机比赛过程与朴素参考实现逐步对照（日志含输入、输出与判定依据）
go test ./swiss/ -run TestRandomDifferential -v

go vet ./...
gofmt -l .
```

差分测试对每条 `Register/Pair/Report/Standings` 操作同时驱动真实实现与
`naive_test.go` 中按规则直写的朴素模型，比对错误类别、对阵、轮空、榜单及
总积分不变量；一旦分歧即打印整组操作日志。

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
