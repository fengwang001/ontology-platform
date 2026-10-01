# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 联赛积分榜

联赛实现位于 `league` 包，队伍编号为 1 到 n（2 <= n <= 32）。`Add(home, away, hg, ag)` 登记有序主客对，`Remove(home, away)` 撤销；写操作使用互斥锁，`Table()` 与 `RankOf(t)` 在一致快照上计算，可并发调用。拒绝顺序与错误哨兵如下：

1. `ErrInvalidTeam`：任一队号不在 1 到 n。
2. `ErrSameTeam`：主客队相同（仅 `Add`）。
3. `ErrInvalidScore`：任一比分小于 0 或大于 100（仅 `Add`）。
4. `ErrMatchExists`：同一有序对已登记（仅 `Add`）。
5. `ErrMatchNotExist`：撤销的有序对未登记（仅 `Remove`）。

比赛胜 3 分、平 1 分、负 0 分；净胜球为进球减失球。首先只按全部比赛的总积分降序分组，总积分相同的队进入同一个并列组。对每个不少于 2 队的组递归执行：

- **甲：相互战绩。** 只统计当前组内部队伍两两之间的比赛，按（相互积分、相互净胜球、相互进球）字典序降序划类。
- **乙：全部比赛总进球指标。** 只有甲无法划开（仅一个等价类）时才执行，按全部比赛的（总净胜球、总进球）降序划类。
- **丙：共享名次。** 甲、乙都无法划开时，当前组内所有队伍并列。

甲或乙划出多个类后，类按指标降序排列；每个不少于 2 队的类重新从甲开始递归，并且该次递归只允许统计该类内部队伍之间的比赛。乙使用的总净胜球和总进球始终来自当前已登记的全部比赛。最终仍在同一并列块中的队伍共享名次；名次等于严格位于其前面的人数加 1，因此可能出现 `1,2,2,4`。并列者按队号升序输出，各总积分并列组按总积分降序依次输出，`Table()` 再按（名次、队号）排序。

同一比赛集合无论按什么顺序登记，得到逐字段相同的 `Table()`；登记后再撤销同一场与从未登记等价。总积分恒等于 `3 × 决出胜负场数 + 2 × 平局场数`。

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

# 联赛规则用例与 2000 组随机朴素对拍
GOCACHE=/tmp/ontology-go-cache go test ./league -v

# 并发竞态检测
GOCACHE=/tmp/ontology-go-cache-race go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

随机对拍固定种子，n 不超过 6；`-v` 日志逐组打印输入比赛、输出积分榜和甲/乙/丙递归判定依据，并与测试内独立写成的直接递归朴素实现逐字段比较。

若当前 shell 找不到 `go`，可将命令中的 `go` 替换为 `/usr/local/go/bin/go`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
