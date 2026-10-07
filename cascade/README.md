# cascade 包

链接级联删除与连锁孤儿清理的参考实现。设计依据与取舍见 `DESIGN.md`。

## 用法

```go
cfg := cascade.NewConfig(map[string]cascade.LinkType{
    "owner": {InRule: cascade.RuleRestrict, OutRule: cascade.RuleCascade, KeepAlive: true},
})
store := cascade.NewMemoryStore()
eng := cascade.NewEngine(store, cfg)
store.AddObject("a")
_ = eng.AddLink(cascade.Link{ID: "l1", Type: "owner", Src: "a", Dst: "b"})

res, err := eng.Delete("a")
```

`res.Deleted` 为受影响对象，`res.Nulled` 为被置空（删除但两端均存活）的链接，
`eng.Log()` 给出每个请求的输入、最终集合与逐步判定依据。

## 测试覆盖

| 测试 | 覆盖要求 |
| --- | --- |
| `TestCycleTerminates` | 链接环路级联删除必然终止、不重复处理 |
| `TestRestrictRollsEverythingBack` | 多链接类型规则并存时一条拒绝导致整体失败且状态还原 |
| `TestMultiRoundOrphans` | 连锁孤儿清理多轮产生新孤儿 |
| `TestOrderIndependence` | 多种内部处理顺序交叉验证最终集合一致 |
| `TestConcurrentDeleteAndAddSerializable` | 并发删除与新建链接交织，按观测全局顺序重放得到同一终态 |
| `TestDifferentialAgainstNaive` | 400 张随机图上与逐条模拟的朴素实现对照（含 4 种朴素顺序） |
| `TestDedupCostIndependentOfGraphSize` | 终止去重探测数不随背景图规模增长 |
| `TestErrorPrecedence` | 三类错误互斥且判定次序固定 |
| `TestLogContainsInputPlanAndRules` | 日志含输入、最终对象集合与每步规则依据 |

```bash
go test -race ./cascade/...
```
