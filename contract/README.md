# contract 包使用指南

合同版本叠加与自动续签服务。时间为整数日序号；所有方法可并发调用。

## 创建合同

```go
svc := contract.NewService()
err := svc.CreateContract(contract.CreateContractInput{
    ID:      "C-1",
    Now:     0,
    StartDay: 0,
    ExpiryDay: 100,
    Parties: [2]contract.PartyInput{
        {ID: "A", AuthFrom: 0, AuthUntil: 10000},
        {ID: "B", AuthFrom: 0, AuthUntil: 10000},
    },
    Clauses: map[string]int{
        "PRICE": 100, "LOCKED_TERM": 1,
        contract.ClauseAutoRenew:   1,
        contract.ClauseRenewalTerm: 30,
        contract.ClauseNoticeDays:  10,
    },
    LockedClauses: map[string]bool{"LOCKED_TERM": true},
})
```

## 协议生命周期

```go
svc.AddAmendment(contract.AddAmendmentInput{
    ContractID: "C-1", AmendmentID: "M1", Now: 5,
    EffectiveDay: 10, Changes: map[string]int{"PRICE": 200},
})
svc.Sign("C-1", "M1", "A", 8)
svc.Sign("C-1", "M1", "B", 9)

// 撤销此前已生效的协议
svc.AddAmendment(contract.AddAmendmentInput{
    ContractID: "C-1", AmendmentID: "R1", Now: 12,
    EffectiveDay: 15, Revokes: "M1",
})

// 触及锁定条款：双方签署后还需法务会签，自会签日生效
svc.LegalCosign("C-1", "M_LOCKED", 20)

// 不续签通知 / 双方协商提前终止
svc.NoticeNonRenewal("C-1", "A", 90)
svc.AgreeEarlyTermination("C-1", "A", 50)
svc.AgreeEarlyTermination("C-1", "B", 50)
```

## 查询

```go
v, _ := svc.EffectiveValue("C-1", "PRICE", 40) // 值 + 来源(MAIN/AMENDMENT)
inTerm, _ := svc.InTerm("C-1", 40)
expiry, _ := svc.CurrentExpiry("C-1", 130)
renewals, _ := svc.Renewals("C-1", 200)
```

## 错误处理

```go
if se, ok := err.(*contract.ServiceError); ok {
    switch se.Code {
    case contract.ErrClockRollback:   // 时钟回退
    case contract.ErrAuthExpired:     // 授权失效
    case contract.ErrMissingCosign:   // 缺少会签（状态化处理，见设计说明）
    case contract.ErrSigningExpired:  // 签署超期
    }
}
```

错误优先级：`INVALID_PARAM > CLOCK_ROLLBACK > NOT_FOUND > ILLEGAL_STATE >
AUTH_EXPIRED > MISSING_COSIGN > SIGNING_EXPIRED`。

## 操作日志

```go
svc.SetLogging(true)
// ... 操作 ...
fmt.Println(svc.SnapshotLog()) // 每步输入/输出/中文判定依据
```

更多语义与取舍见 `DESIGN.md`。
