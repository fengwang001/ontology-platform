# 循环赛赛程与赛果登记器

Go 包 `ontology/tournament` 使用圆法生成单循环或双循环赛程，支持主客场、轮空、比分登记、退赛技术判负、积分与净胜球查询。所有变更和查询均通过读写锁串行化，因此并发调用等价于某个合法的串行执行顺序。

## 构造与 API

```go
t, err := tournament.New(4, 1) // N=4，单循环；L=2 时为双循环

t.Fixtures(1)                 // 按 i 升序返回该轮对阵
err = t.Record(1, 1, 4, 2, 1) // 第 1 轮主队 1、客队 4，比分 2:1
err = t.Withdraw(1)           // 队 1 退赛，未赛场判对手 3:0

t.Points(1)    // 积分：胜 3、平 1、负 0
t.GoalDiff(1)  // 进球 - 失球
t.Played(1)    // 已登记场数 + 技术判定场数，不含轮空
t.Pending()    // 尚未登记且未技术判定的比赛数
```

`Fixture` 字段为 `Round`、`Home`、`Away`、`Bye`。`Bye=true` 表示真实队伍本轮轮空；该场不是比赛，不能登记比分，也不计 `Played`。

## 圆法与主客场

- 仅接受 `2 <= N <= 64` 且 `L in {1,2}`，否则返回 `ErrInvalidConfig`。
- `m` 为不小于 `N` 的最小偶数；`N` 为奇数时加入虚拟队 `N+1`。
- 初始位置为 `p=[1,2,...,m]`，第 `r` 轮先按当前位置取 `a=p[i]`、`b=p[m-1-i]`，`i=0..m/2-1`。
- 每轮后排程后旋转为 `[p[0], p[m-1], p[1], ..., p[m-2]]`，`p[0]` 始终不动。
- `i=0`：`r` 为奇数时 `a` 主队，偶数时 `b` 主队。
- `i>=1`：`r+i` 为偶数时 `a` 主队，否则 `b` 主队。
- 单循环为 `m-1` 轮；双循环增加同样数量轮次，第二圈复用第一圈对阵但交换主客。

## 退赛技术判负

`Withdraw(t)` 会把该队所有尚未判定的比赛立即判为对手 `3:0`：对手获得 3 分、3 个进球、0 个失球；退赛队获得 0 分、0 个进球、3 个失球，双方各计一场。已经人工登记的比分原样保留。若对手此前已经退赛，该场已经被判定，不会再次计分。

`Record` 按以下顺序只返回第一个错误：

1. `ErrFixtureNotFound`：轮次不存在、主客方向不匹配，或尝试登记轮空。
2. `ErrInvalidScore`：任一比分小于 0 或大于 100。
3. `ErrAlreadyTechnical`：该场已经因退赛技术判定。
4. `ErrAlreadyRecorded`：该场已经人工登记。

`Withdraw` 的队号错误返回 `ErrInvalidTeam`，重复退赛返回 `ErrTeamAlreadyWithdrew`。被拒绝的操作不会修改比分、积分、场次或待判定数量。

## 本地验证

如当前 shell 找不到 Go，可使用 `/usr/local/go/bin/go`；如默认构建缓存只读，可将缓存指向 `/tmp`：

```bash
export GOCACHE=/tmp/go-cache-ontology

# 全量测试
/usr/local/go/bin/go test ./...

# 竞态检测与详细日志：赛程逐轮朴素对照、输入输出和判定依据都会打印
/usr/local/go/bin/go test -race -v ./tournament

# 代码检查
gofmt -w tournament
/usr/local/go/bin/go vet ./...
```

测试覆盖 N=4、5、6 的逐场赛程、两类主客规则、奇数队轮空、N=2..64 与朴素重生成实现逐一对照、双循环主客互换、退赛技术判负、错误优先级、拒绝不变性、并发串行等价和相同操作序列重放。
