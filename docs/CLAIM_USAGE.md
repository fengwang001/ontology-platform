# 理赔分摊系统 — API 与本地验证

## 包

- 生产实现：`ontology/internal/claim`
- 朴素参考模型：`ontology/internal/naive`（同构 API，供差分测试）

## 核心 API

```go
s := claim.NewSystem()

err := s.AddPolicy(claim.Policy{
    ID: "P1", Subject: "car",
    Limit: 1000, Deductible: 100,
    StartDay: 0, EndDay: 365,
    Insurer: "A",
    Clause: claim.Ordinary, // 或 claim.Excess
})

res, err := s.RegisterAccident(claim.Accident{
    ID: "A1", Subject: "car", Day: 10, Loss: 500,
})
// res.Seq        登记序号（从 0 开始）
// res.TotalPaid  所有保单纯赔付之和（<= 损失）
// res.Payments   各保单赔付（保单编号已排序）

res2, err := s.CorrectAccident(claim.CorrectAccidentInput{
    AccidentID: "A1", NewLoss: 620,
}) // 返回更正后该事故结果；其后事故已连锁重算

err = s.CancelPolicy(claim.CancelPolicyInput{
    PolicyID: "P1", CancelDay: 20,
}) // 注销日 20 当天及以后“新登记”的事故不再受其覆盖

r, err := s.AccidentResult("A1")
rem, err := s.RemainingLimit("P1")

ok := s.ReplayConsistencyOK() // 从头重放并逐事故/逐保单核对
_ = s.Stats()                  // 内部工作量计数器（复杂度证明用）
```

## 错误判定

所有错误都用 `errors.Is` 判定，优先级如下：

```go
errors.Is(err, claim.ErrInvalidArg)      // 参数非法（最高优先级）
errors.Is(err, claim.ErrDuplicateID)     // 编号重复（保单/事故共用命名空间）
errors.Is(err, claim.ErrNotFound)        // 事故或保单不存在
errors.Is(err, claim.ErrPolicyCancelled) // 注销已注销保单
```

重点规则：

- 无任何覆盖保单的事故登记**成功**，赔付为 0，不是错误；
- 免赔额恰等损失额 → 独立赔付额 0 → 普通 `S=0` → 不赔；
- 注销日不得不晚于到期日，且不得早于该保单覆盖过的任一事故发生日
  （是否真正赔到钱不影响“覆盖过”的判定）。

## 本地验证

```bash
# 全量测试（若 go 不在 PATH，先 export PATH=$PATH:/usr/local/go/bin）
go test ./...

# 竞态 + 详细日志（日志打印每个随机操作的输入、输出与错误类别）
go test -race -v ./internal/claim/

# 只看复杂度证明（含 VERDICT 日志）
go test -run 'TestRegistrationCost|TestCorrectionCost' -v ./internal/claim/

# 生产分摊 vs 朴素逐单位法、生产引擎 vs 朴素模型
go test -run 'TestShareGroupMatchesUnitSteps|TestRandomDifferential' -v ./internal/claim/

# 基准
go test -bench BenchmarkShareGroup -run XXX ./internal/claim/

go vet ./...
gofmt -l .
```

## 测试清单与覆盖的需求点

| 测试 | 覆盖点 |
| --- | --- |
| `TestDeductibleEqualsLoss` | 免赔额恰等损失额，独立赔付 0，不赔 |
| `TestLimitExactlyExhausted` | 剩余保额恰好耗尽后无赔可付 |
| `TestRemainderOrdering` | 取整余数按 (生效日, 编号) 补给 |
| `TestExcessStaysOutWhenOrdinaryCoversLoss` | 普通足额时超额不介入 |
| `TestExcessStepsIn` | 普通赔不足时超额在未补偿范围内赔付 |
| `TestCorrectionCascades` | 更正使后续事故连锁变化，且与全量重放一致 |
| `TestCancelDayEqualsAccidentDay` | 注销日恰等事故日：历史不变，当天新登记不覆盖 |
| `TestCancelValidation` | 注销日早于已覆盖事故 → 参数非法；重复注销 → 已注销 |
| `TestNoCoveragePaysZero` | 无覆盖保单登记成功且赔付 0 |
| `TestRejectedOperationsLeaveNoTrace` | 被拒绝操作不留痕 + 错误优先级 |
| `TestTreapMatchesBruteForce` | 区间索引 4000 步随机增删与暴力扫描一致 |
| `TestShareGroupMatchesUnitSteps` | 生产分摊与逐单位朴素法 2000 组随机一致 |
| `TestRandomDifferential` | 120 条随机操作序列与独立朴素模型全状态对照 |
| `TestConcurrentSerializable` | 并发竞争添加/注销/只读，`-race` 下唯一赢家 + 限额不超额 |
| `TestRegistrationCostIndependentOfAccidentCount` | 登记工作量不随事故总数增长 |
| `TestCorrectionCostOnlyAffected` | 更正工作量只与受影响事故数有关 |

随机对照日志在 `-v` 下逐条打印 `AddPolicy/Register/Correct/Cancel` 的输入、
返回的规范化结果（序号、总额、各保单纯赔付）或错误类别（`invalid/dup/
missing/cancelled`），判定依据为“生产引擎与朴素模型返回类别与全量可观察
状态必须逐字节一致”。
