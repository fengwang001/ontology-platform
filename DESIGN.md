# 不停库循环盘点差异处理器 — 设计说明

1. 目标：盘点期间 `Move` 不冻结；初盘/复盘/三盘阶段可精确重放；超容差走审批。
2. 三个包：`adjust`（账本/容差/错误/锁）、`count`（任务与阶段机）、`authz`（权限与审批）。
   `count`、`authz` 直接操作 `adjust.System` 的加锁内存状态，避免接口重复与循环依赖。
3. 放弃的方案 A（盘点期间冻结库位）：会阻塞日常出入库；改用累计净移动量 `mv` 校正——
   两次实盘之差恰等于 `mv-m1` 即判定差异真实，无需遍历流水。
4. 放弃的方案 B（保存全部移动流水，复盘时逐条求和）：一致性判定退化为 O(笔数)。
   本设计只用两个标量快照 `m1`/`m2`，判定读取流水 0 条；`scanned` 非导出计数器
   可在测试中通过快照方法证明 Submit 触碰记录数为常数（10 笔与 10000 笔对照）。
5. 放弃的方案 C（审批通过后 `book=counted`）：申请之后仍可能有出入库，会冲掉期间移动；
   改为 `book += diff`（diff 为提交时刻 counted-book），语义为"修正账面差异"。
6. 容差在每次判定瞬间用当前 book 计算 `tol=max(Tabs, floor(book*Tpct/100))`，恰等算容差内。
7. 并发：全局单一 RWMutex（操作均含读改写，用 Lock），每个公开操作等价一次原子临界区，
   结果等价于某串行顺序；纯 int64 运算（最大约 1e15）不溢出。
8. 每个库位一个状态记录（阶段、三轮实盘/盘点人、mv 快照、待批差值）；任务持有库位集合，
   `locs` 反查保证库位至多在一个未关闭任务中。
9. 错误为哨兵错误（ErrInvalid/ErrNotFound/ErrState/ErrConflict/
   ErrNoApprove/ErrMustSwitch/ErrNoSenior/ErrUnderstock），按题给次序只报第一个，
   拒绝前不落任何状态。
10. 调整量单独累计 `adjSum`：不变量 `book == 初始 book + Σ接受Move + Σ调整` 可逐条校验。
11. 审批金额 `|diff|*price` 恰等 Lim 不需 Senior；审批人不得是任一轮盘点人。
12. Submit 不需权限；Approve/Reject 需 Approve，超 Lim 另需 Senior；Reject 不调账直接 Done。
13. 审批时若 `book+diff<0` 报库存不足，申请保持 Pending，可在补货后重试。
14. Close 要求全 Done；关闭仅释放库位占用与任务状态，库位账本与 mv 连续保留，可再盘点。
15. 本地验证：`go test ./... -race -v`；表驱动用例覆盖题面全部边界；
   另以"保存全部流水逐条求和"的朴素模拟器对 1500 组随机序列核对 book 与阶段迁移，
   `-v` 日志打印每组输入、输出与判定依据。
