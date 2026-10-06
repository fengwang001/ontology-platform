# 预约单排程系统 — 设计说明

1. 时刻为 int64 秒；系统维护“已接受操作最大时刻”，仅在操作通过全部校验后才提交，
   故被拒绝操作不推进时钟，回退报 ErrClockRollback。
2. 名额账本：每时段维护 capacity 与 occupied；occupied 只对“未取消且未送达”计数，
   释放后仍占名额。overbooked = occupied > capacity；余量 = occupied < capacity，
   因此超额与满都拒绝新单，超额时段自然不进顺延候选；调低不驱逐、调高立即生效。
3. 释放不写状态：released = now >= start-DispatchLead（恰等即释放），
   是时刻的纯函数，任何查询/操作自带的 now 即可精确复现，判定 O(1)。
4. 顺延：从原时段之后第一个候选起线性扫描，候选需 start-orig<=MaxPostponeSpan（恰等允许）、
   start-now<=MaxBookAhead、且有余量；只向后；扫描量取决于时段数而非预约总数。
5. 改期：状态类错误先于截止线、提前量、名额；截止 now>=start-RescheduleLead（恰等拒绝）；
   改期不触发顺延；成功时“新占+旧放”在同一临界区原子完成，失败原名额不动。
6. 并发取舍：选单把 sync.Mutex——临界区只有内存计算，天然等价于某种串行顺序，
   改期原子性与“非超额不超上限”直接成立。放弃分片锁/分段事务（无 I/O，复杂度不划算）、
   放弃后台释放扫描（释放改为时刻纯函数，避免扫描并消除遗漏窗口）。
7. 放弃的方案：给释放打标记字段（会产生“无人触碰就不释放”的不可复现窗口）、
   顺延时扫描全部预约求余量（违反不随预约数增长的要求）。
8. 性能证据：BenchmarkPostponeIndependentOfOrderCount 与 BenchmarkReleaseCheckConstant
   在 1 万笔预约上仍为百纳秒/单与几十纳秒/单，且候选搜索与释放判定均不遍历预约。
9. 对照模型 naive.go 不维护 occupied，每次全量扫描预约计数；与主模型逻辑独立编写。
10. 本地验证：GOCACHE=/tmp/go-cache go test -race ./booking/；
    go test -run TestRandomDifferential -v 查看每步输入/输出/判定依据日志；
    go test -run xxx -bench . -benchtime 10000x ./booking/ 查看基准。
