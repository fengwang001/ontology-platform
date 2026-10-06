# 分摊赔付引擎（ontology 包）

同一损失下多张保单重复投保时的分摊裁定，支持三种条款混合：

- `ClauseLimitProportional` 限额比例型：权重为 `min(每次损失限额, 年度累计剩余)`。
- `ClauseIndependentLiability` 独立责任型：出现任一张即令阶段一全部按独立责任额比例。
- `ClauseExcess` 超额型：仅在非超额保单未赔足时于阶段二参与。

## 使用

```go
e := ontology.NewEngine()
err := e.RegisterPolicy(ontology.Policy{
    Insured: "I", PolicyID: "P1",
    Deductible: 100, PerLossLimit: 1000, AnnualLimit: 10000,
    CoverFrom: 0, CoverTo: 365, Clause: ontology.ClauseLimitProportional,
})
r, err := e.AcceptLoss(ontology.Loss{LossID: "L1", Insured: "I", Day: 30, Amount: 800})
// r.Shares / r.Paid / r.NoPayer；r.Reason 为判定依据日志
err = e.CancelLoss("I", "L1") // 仅末笔可撤销，恢复年度累计
```

金额单位均为非负整数「分」；错误用 `*ontology.EngineError`，按
`ErrInvalid > ErrInsuredMissing > ErrPolicyDuplicate > ErrLossExists >
ErrLossMissing > ErrNotLast` 固定次序判定。

规则与取舍见 `DESIGN.md`。
