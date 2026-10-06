# remittance

跨境汇款报价锁汇、限额（单笔 / 自然日 / 滚动 365 天年度）占用与合规审核。

- 时间为整数秒；所有操作携带 `now`，回退即报 `ClockBackward`，被拒操作不改时钟与状态。
- 报价自申请起有效 `T` 秒：到期恰等仍有效，晚 1 秒过期；最多使用一次。
- 目标额 `floor(src*ratePPM/1e6)`，占用额 `ceil(src*ratePPM/1e6)`（128 位整数运算）。
- 目标额达到审核阈值进入待审核：占用已计、未出款；`R` 秒内（含第 R 秒）批准则出款，拒绝/撤回/逾期失败并从**原占用日**释放。
- 制裁名单命中一律拒绝，不消耗报价与幂等键；同键同参返回首次原结果，同键异参报冲突。
- 单锁线性化；提交/查询开销与该汇款人历史汇款总数、汇款人总数无关（窗口桶 ≤ 365）。

## API

| 方法 | 说明 |
| --- | --- |
| `New(Config)` / `AddSender` / `AddSanctionedPayee` | 初始化与管理 |
| `ApplyQuote(QuoteRequest)` | 申请锁汇报价 |
| `Submit(SubmitRequest)` | 提交汇款（幂等键、报价、收款人、now） |
| `Approve` / `Reject` / `Withdraw` | 人工审核与撤回 |
| `GetTransfer` / `Usage` | 状态与日/滚动年度占用查询（会物化逾期） |

错误通过 `CodeOf(err)` 取 `ErrorCode`，提交优先级：
参数非法 > 时钟回退 > 制裁 > 幂等冲突 > 报价不存在/已消耗 > 报价过期 >
单笔 > 日 > 年度；审核/撤回：参数 > 时钟 > 不存在 > 状态不允许。

## 验证

```bash
go test ./...                                   # 含 40 种子 × 400 操作朴素模型对照
go test -v -run TestDifferentialRandom ./remittance/  # 查看每步输入/输出/判定日志
go test -race -count=3 ./remittance/            # 并发与时钟
go test -run=NONE -bench=. -benchmem ./remittance/    # 复杂度证据
```

详见 [DESIGN.md](DESIGN.md)。
