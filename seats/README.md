# seats：多航段联程座位库存与超售控制

一条行程由 1–4 个航段按顺序组成；每个航段有物理座位数、超售上限与三
个有序嵌套的舱位等级（0 最高、2 最低），同一航段可被多条行程共用。
系统对预占、出票、取消、授权量调整给出可精确复现的结果。设计取舍见
[DESIGN.md](DESIGN.md)。

## 快速开始

```go
sys, _ := seats.NewSystem(3600) // 预占时长 3600 秒

// 注册航段：标识、物理座位数、超售上限、三等级授权量（高>=低，最高<=座位+超售）、当前时刻
_ = sys.AddSegment("PEK-SHA", 100, 10, [seats.NumClasses]int{110, 80, 50}, 0)
_ = sys.AddSegment("SHA-CTS", 80, 0, [seats.NumClasses]int{80, 60, 40}, 0)

// 预占：行程 + 旅客人数(1-9) + 当前时刻，全有或全无
id, expiry, err := sys.Hold([]seats.Leg{
	{Segment: "PEK-SHA", Class: 0},
	{Segment: "SHA-CTS", Class: 1},
}, 2, 100)

// 出票 / 取消（对已过期预占报 KindHoldExpired，与 KindNotFound 可区分）
_ = sys.Ticket(id, 200)
_ = sys.Cancel(id, 300)

// 授权量调整（允许低于已占用，不得破坏嵌套次序）
_ = sys.AdjustAuth("PEK-SHA", 2, 45, 400)

// 只读查询（不参与时钟约束）
avail, _ := sys.Availability("PEK-SHA", 0)
ok, firstBad, _ := sys.CanHold([]seats.Leg{{Segment: "PEK-SHA", Class: 0}}, 9)

// 导出 / 恢复（未过期预占保留原到期时刻）
snap := sys.Export()
restored, _ := seats.Restore(snap)
_, _, _ = avail, ok, restored
```

## 错误分类

所有被拒绝的操作返回 `*seats.Error`，`Kind` 按以下次序只报最靠前的一
类：`KindInvalidParam` > `KindClockRollback` > `KindNotFound` >
`KindHoldExpired` > `KindStateConflict` > `KindInsufficient`。被拒绝的
操作不改变任何占用、授权量、时钟或条目状态。

## 测试

```bash
go test ./seats/ -v        # 定向用例 + 朴素模型随机比对（逐步日志）
go test -race ./seats/     # 并发与竞态
```
