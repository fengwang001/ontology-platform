# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 联赛积分榜

`NewLeague(n)` 创建队伍编号为 `1..n` 的联赛，`n` 必须在 `2..32`。核心接口：

- `Add(home, away, homeGoals, awayGoals)`：登记一场主队 `home` 对客队 `away` 的比赛；同一有序对 `(home, away)` 至多登记一次。
- `Remove(home, away)`：撤销该有序对已登记的比赛；撤销后的状态与该场从未登记等价。
- `Table()`：返回每队的名次、总积分、总净胜球、总进球，按 `(名次, 队号)` 排序。
- `RankOf(team)`：返回指定队伍名次。

胜场计 3 分，平局计 1 分，负场计 0 分；净胜球为进球减失球。登记、撤销和查询使用读写锁保护，并发结果等价于某个合法串行顺序。

### 并列递归裁决

先按全部比赛的总积分降序分组。对每个至少包含两队的并列组 `G` 执行 `Rank(G)`：

1. **甲（相互战绩）**：只统计 `G` 内队伍两两之间、双方都在当前组中的已登记比赛，按 `(相互积分, 相互净胜球, 相互进球)` 字典序降序划等价类。若至少两类，按类顺序输出；每个仍至少两队的类重新从甲开始递归。
2. **乙（全局净胜球）**：仅当甲只有一类时使用全部比赛的 `(总净胜球, 总进球)` 降序划等价类。若至少两类，按类顺序输出；每个仍至少两队的类同样重新从甲开始递归。
3. **丙（并列）**：甲、乙都只有一类时，当前组全部并列。

递归中的相互战绩只看当前类内部队伍之间的比赛，不退回全部比赛。共享名次等于严格排在该并列组前面的队伍数加 1，并跳过并列占用的名次，例如 `1,2,2,4`；同一并列组内按队号升序输出，各积分组按总积分降序处理。

### 拒绝原因

`Add` 按以下顺序只返回第一个错误：

1. `ErrInvalidTeam`：任一队号不在 `1..n`。
2. `ErrSameTeam`：主客队相同。
3. `ErrInvalidScore`：任一比分小于 0 或大于 100。
4. `ErrMatchAlreadyAdded`：该有序对已登记。

`Remove` 返回 `ErrInvalidTeam`（队号越界）或 `ErrMatchNotAdded`（该有序对未登记）。被拒绝的操作不会修改现有比赛集合。

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

# 联赛积分榜：查看全部定向场景与 2000 组随机朴素递归对拍日志
go test -v ./...

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
