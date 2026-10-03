# 设计说明：异构库逐行对账归一化比较与修复计划器

1. 分三个包：norm（纯函数：Cvt、TrimC 与参数校验）、diff（并发安全的两侧内存表、
   版本计数、Compare 归并、供 plan 包复用的事务原语）、plan（Plan 生成与 Apply 原子应用）。
2. 比较口径取舍：先把源值按目标标度 sd 与舍入模式 rm 归一后再比较；放弃“比较原始值
   +容差”方案——容差阈值随标度漂移、无法区分半入/半偶/截断、且不能产生可直接落目标
   端的确定值。归一后比较的结果与 Insert/Update 实际写入的值同源，收敛性可精确复现。
3. NULL 表示：d 用 *int64 可空尾数，c 用 []byte 可空字节串（nil=NULL，空 slice=空串）。
   c 仅去除右侧 0x20，保留左空格与制表符；ne=true 时空串归一为 NULL，NULL 与空串等价。
4. 舍入：q=trunc(m/D)、r=m-qD；rm=0 半入（2|r|>=D 进位，方向同符号）；rm=1 半偶
   （2|r|>D，或等于 D 且 q 奇）；rm=2 截断。sd=6 时 D=1，Cvt 恒等。中间量均在 int64 内。
5. 版本：每个 id 一个单调计数器；TgtPut/TgtDel（及 Apply 写入）各加 1，删除不归零。
   Plan 每项快照记录生成时刻版本（Insert 记“目标不存在”），Apply 据此乐观并发校验。
6. 应用语义取舍：整计划先全部校验（不存在/存在且版本相等）后一次性提交，任一失配整体
   ErrStale 且零改动；放弃逐行尽力 best-effort——部分应用会让重试不可判定、破坏
   “Apply 后 Compare 收敛”的不变量。ErrStale 报计划中 id 最小的失配项，保证确定性。
7. Extra 取舍：默认只报告（Compare 永远只读），仅 Plan(...,del=true) 才生成 Delete；
   放弃默认删除——对账工具默认不得销毁目标端数据。含 Delete 的计划仅 role=2 管理员可应用。
8. 拒绝次序：参数非法（sd/rm 越界、id/尾数/字节长度越界、plan 为 nil、区间非法）→
   Apply 权限不足（role 不属于 {1,2}，或含 Delete 而 role 不为 2）→ ErrStale；拒绝不改状态。
9. 并发：diff.Engine 用 sync.RWMutex 保护 map，Compare 持读锁整体归并，结果等价于某串行
   顺序；Apply 的“校验+提交”在写锁内完成。Plan 生成值与版本快照，是不可变拷贝。
10. 可测性：diff 提供非导出的区间行访问计数（两侧范围内行数之和），内部测试白盒断言
    1000/100000 两档；2000 组随机对拍以朴素逐步模拟（独立 map 重算）为参照并打印日志。

## 本地验证方法

    go test ./...                  # 全量单测（2000 组随机对拍、收敛、ErrStale 用例）
    go test -race ./...            # 并发安全
    go test -v ./plan -run Random  # 随机对拍的输入/输出/判定日志
    go vet ./... && gofmt -l .
