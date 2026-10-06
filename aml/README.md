# aml — 反洗钱现金存款结构化拆分检测

识别客户把大额现金拆成多笔小额存入以规避大额申报的行为。
设计取舍与复杂度证明见 [DESIGN.md](DESIGN.md)。

## 快速开始

```go
e, err := aml.NewEngine(aml.Config{Low: 100, High: 1000, K: 3, D: 7})
if err != nil {
	// 配置非法（须满足 0 < L < H、K >= 2、D >= 1）
}

_ = e.AddAccount("alice", 0)                 // 注册账户，初始自成一组
_ = e.AddAccount("bob", 0)

r1, _ := e.Deposit("tx-1", "alice", 400, 1)  // 小额：进入结构化判定
r2, _ := e.Deposit("tx-2", "alice", 1000, 2) // 大额：立即产生大额报告
r3, _ := e.Link("alice", "bob", 3)           // 关联：合并客户组并评估
_ = e.Reverse("tx-1", 4)                     // 冲正：作废，不撤回已发报告
_, _, _ = r1, r2, r3

accounts, _ := e.GroupAccounts("alice")   // 当前客户组全部账户
count, sum, _ := e.CurrentSet("alice", 4) // 按当前 now 的判定集合
reports := e.Reports()                    // 已发出的全部报告
_, _, _ = accounts, count, sum
_ = reports
```

## 规则摘要

- 单笔 >= `High`：立即产生大额报告，不参与结构化判定。
- `Low` <= 金额 < `High`：小额，进入所属客户组的窗口判定集合。
- 金额 < `Low`：只记录，不参与任何判定。
- 判定集合：组内未冲正、且 `now - 存款日期 < D` 的小额存款；
  笔数 >= `K` 且合计 >= `High` 时成立。
- 成立且集合中仍有未被覆盖的存款时发出一份结构化报告，并覆盖集合内全部存款；
  同一批存款不重复报告。
- 每次被接受的操作最多产生一份报告；报告按产生次序全局编号。
- 所有操作携带单调不减的 `now`；被拒绝的操作不留任何痕迹。
- 错误优先级：参数非法 > 时钟回退 > 账户不存在 > 交易号重复/不存在/已冲正 > 已关联。

## 测试

```bash
go test ./aml/ -race -v
```
