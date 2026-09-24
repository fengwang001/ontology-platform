# 事务发件箱中继 NOTES

## 十步推导（规则叠加结果）

| 步 | 分配 | 本步投递 | 已提交未标记(投递序) | 已应用 | 重复 |
|---|---|---|---|---|---|
| 1 Write(T1) | id=1 | — | — | [] | 0 |
| 2 Write(T2) | id=2 | — | — | [] | 0 |
| 3 Commit(T2) | csn=1 | — | [2] | [] | 0 |
| 4 Relay(false) | 无 | [2] | [] | [2] | 0 |
| 5 Write(T3) | id=3 | — | [] | [2] | 0 |
| 6 Write(T1) | id=4 | — | [] | [2] | 0 |
| 7 Commit(T3) | csn=2 | — | [3] | [2] | 0 |
| 8 Commit(T1) | csn=3 | — | [3,1,4] | [2] | 0 |
| 9 Relay(true) | 无 | [3,1,4] | [4] | [2,3,1,4] | 0 |
| 10 Relay(false) | 无 | [4] | [] | [2,3,1,4] | 1 |

- (甲) 游标=已标记最大 id（第 4 步后=2）。第 9 步只取 id>2：投递 [3,4]；十步后已应用=[2,3,4]；id=1 已提交但 id<游标，永远到不了下游。不用游标、仅批内按 id 排序：第 9 步投递顺序为 [1,3,4]。
- (乙) 先标记后投递：第 9 步对最后一条 id=4 先标记、崩溃在投递前 → 投递 [3,1]；第 10 步批为空，投递 []。已应用=[2,3,1]，重复=0，丢 id=4（已标记却未投递）。
- (丙) 记「已应用最大 id」判重：第 9 步 id=1 ≤ maxid=3 被误判为重复。十步后已应用=[2,3,4]，重复=2。正确实现：十步共投递 5 次，重复=1。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`obx.Commit` 把事务消息按 csn 追加进 pending（天然 (csn,id) 有序），`rly.Relay` 按批序处理 — `api_test.TestNaiveReference`。
2. 标记蕴含投递：`rly.Relay` 每条先 `deliver` 后 `box.Mark`，崩溃只可能漏标不可能漏投 — `api_test.TestMarkImpliesDelivered`。
3. 恰好应用一次：`rly.deliver` 用 seen 集合判重，重复只计 dups — `api_test.TestNaiveReference`（模型 id 唯一，逐项相等即无重复）。
4. 失败不留痕：`obx.Write/Commit/Abort` 先完成全部校验再改状态，id/csn 在校验通过后才递增 — `api_test.TestRejectKeepsState`。
