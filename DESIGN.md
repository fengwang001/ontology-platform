设计说明：按作用域注入密钥并对日志打码

1. 三包划分：secret 负责定义/仓库/环境的存储与版本；scope 只放纯规则（名字与值校验、分支模式匹配、可见性、保护判定）；inject 负责作业解析、快照与 Mask。inject 依赖 secret、scope，secret 不依赖 inject。
2. 错误模型：ErrInvalid、ErrRepoNotFound、ErrEnvNotFound、ErrExists、ErrSecretNotFound、ErrJobNotFound，以及失败原因 ErrNotFound/ErrWithheldFork/ErrWithheldProtected。哨兵值配合 fmt.Errorf("%w") 用 errors.Is 区分。
3. 拒绝次序：参数非法 > 仓库/环境不存在 > 已存在(Add*) > 密钥不存在(Delete)。Inject 的整体检查次序：参数非法 > 仓库不存在 > 环境不存在 > 环境不允许（环境分支判定先于 fork 扣留，fork 不查任何存储）。
4. 层级解析：env(仅 env 非空)→repo→org 取第一个存在的定义；组织级对本仓库不可见视同不存在。被扣留(Protected/Fork)即终止，不回落到更宽层级——因为作用域是“最具体定义是否允许用于本上下文”，回落会泄露更宽凭据，且仓库管理员无法借 protectedOnly 阻断组织级密钥使用。
5. 放弃方案：曾考虑“自左向右最长匹配”打码；放弃，改用全部出现区间求并集（重叠或首尾相接合并为极大区间，整段换 ***），否则 abab 在 xababab 中会留下可还原片段，abcdabcd 相接时也漏出边界。
6. 快照：作业创建时复制 Found 值；Set/Delete 只影响后续作业，Mask 用快照值。只有长度 ≥4 的快照值参与打码；pr_fork 作业快照恒为空。
7. 一致性：secret.Store 用 RWMutex，Inject 在一次会话中持 RLock 取只读视图，保证一次 Inject 看到同一时刻的全部定义（线性化点为锁获取）；服务自身用互斥锁分配作业号。锁序固定为 inject.jobMu → store 锁，避免死锁。
8. 探测计数：每名字按层级各查一次（env/repo/org），org 不可见仍算一次探测，故每名字 ≤3；pr_fork 直接返回 Withheld(Fork)，探测为 0。probes 为非导出计数器，白盒测试读取，并用 100 与 10000 个密钥两档 benchmark 对照。
9. 模式：末尾 '*' 为前缀匹配（"*" 匹配一切），否则精确相等；'*' 出现在其他位置属参数非法。保护分支仅 push 事件参与判定，两种 PR 一律不保护。
10. 本地验证：go build ./...；go test -race ./...；随机 1500 组操作序列与独立编写的朴素逐步模型对照，日志打印输入、输出与判定依据。
