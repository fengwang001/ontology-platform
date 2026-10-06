# 设计说明（机票改签差价与退票阶梯结算）

1. 金额单位为分（int64 非负），时刻为非负秒；手续费 = `ceil(fare*pct/100)`。
2. 模块划分（共 6 个实现文件，职责单一）：
   - `errors.go`：错误类别 `ErrorKind`（按统一拒绝次序编号）与 `OpError`。
   - `fee.go`：档位划分 `tierFor`（远/中/近，恰等于阈值归宽松档）与向上取整。
   - `system.go`：`Config/Flight/Ticket/Voucher/System`、时钟、注册、购票、航司取消。
   - `voucher.go`：代金券确定性生成、四类校验（不存在/归属/过期/用尽）、面额核减。
   - `refund.go`：自愿（票面-手续费）与非自愿（票面+历次改签费）退票及报价。
   - `change.go`：改签费用结构、支付校验、代金券抵扣、落地提交及报价。
3. O(1) 关键取舍：票上增量保存 `Fare/Departure/ChangeFees/Changes/CashPaid/CashRefunded`，
   退票与改签报价只读单张票、不遍历历史；`TestQuoteO1` 实测 10 次 vs 1000 次改签、
   10 张 vs 10000 张票，报价 ns 比值约 1.1，benchmark 中报价 0 分配。
4. 并发：单把 `sync.Mutex` 串行所有变更，线性化且可复现；读写均加锁。查询用同一把锁，
   放弃了分片锁/RW 优化以换取规则正确性优先（临界区为纯内存 O(1)，无需更复杂方案）。
5. 时钟：被拒绝操作“先检查后提交”——全部校验通过才 `s.now=now` 并落地，杜绝拒绝推进时钟。
6. 拒绝次序集中在一条校验链：参数非法 > 时钟回退 > 票不存在 > 已退 > 已出发 >
   次数超限 > 代金券四类 > 支付不符；注意“已出发”先于“次数超限”，非自愿票两者均跳过。
7. “未退未改”解释为“当前仍在该航班上且未退票”（含曾改签到本航班的票），取消即按当前归属标记。
8. 非自愿改签：费用/差价/券全部不发生（应补为 0，带现金或券直接支付不符），改后清非自愿标记、
   不计次；代金券不随非自愿退票返还。
9. 代金券 ID 由单调序号生成（`V000001…`），相同操作序列重放得到相同标识。
10. 被放弃方案：按事件流逐笔重算（实现简单但报价 O(n)）、每券核减生成新券（破坏“差额留原券”）、
    多版本时间戳无锁（复杂度高且本题无吞吐压力）。
11. 独立朴素模型 `internal/naive` 刻意不做汇总缓存，每次从历史重算，与主系统差分比对。
12. 本地验证：
    `GOCACHE=/tmp/gocache go test -race ./...`
    `GOCACHE=/tmp/gocache go test -run TestNaiveDiffRandom -v .`（打印每步输入/输出/判定）
    `GOCACHE=/tmp/gocache go test -run TestQuoteO1 -v .`（O(1) 证据）
    `GOCACHE=/tmp/gocache go test -bench BenchmarkQuoteChangeConstant .`
