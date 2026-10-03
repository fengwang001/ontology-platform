# 多租户对象存储桶：设计说明

1. 分三个包：`version`（版本链/当前版本/删除标记）、`lock`（保留期+法律保留，纯状态机）、`authz`（权限位集合）；顶层 `package bucket` 做规则编排（拒绝次序、审计、时钟、批量原子性），避免规则散落。
2. 版本链：全桶单调序号由 bucket 的 `seq` 分配，Put 与无 ver 的 Delete 各占一号，被拒绝的操作在分配前返回，故不占号、无洞。
3. 每键双向链表，记录内嵌 prev/next，另存 current 指针；删除当前版本只需触碰当前节点与其前驱（≤2），与版本总数无关。记录访问统一经 touch() 计数；touched 非导出计数器证明复杂度（包内测试可见）。
4. 被放弃方案：按版本号排序切片找当前版本（每次 O(n)，touched 不可证 ≤2）；为“当前”另建索引（删除后仍需扫描）。
5. 到期保留视同无锁：active(now) 定义为 retainUntil>now，now==retainUntil 即到期；COMPLIANCE 到期后可任意重设（含缩短）。旧值不物理清除，判定与新设置均视为无保留。
6. SetRetention 迁移规则集中在 lock：生效 COMPLIANCE 只能保持 COMPLIANCE 且 until 不小于原值；生效 GOVERNANCE 可保级/升级且不缩短，其余需 BypassGovernance；NONE 清除。
7. 并发：bucket 单一互斥串行化全部写与审计；结果等价于某串行顺序，无需细粒度锁，重放确定性强。
8. DeleteVersions 选全有或全无而非部分成功：先批级检查（参数/时钟/权限），再对每项基于批开始快照判定，全通过才按下标升序执行并统一推进时钟、写审计；批内重复 (key,ver) 为参数非法。
9. 拒绝次序统一：参数非法 > 时钟回退 > 权限 > 版本不存在 > 法律保留 > 合规保留 > 治理保留；每次只报第一个原因。批量失败原因带最小下标。
10. 审计仅记录“生效 GOVERNANCE 且确因 bypass+BypassGovernance 才放行”的永久删除（含批量），记 key/ver/now/旧 retainUntil；无保留、已到期、带 bypass 却并不需要的不记。
11. 标记永远可删（无锁）；标记不可作为 SetRetention/SetHold 目标（按版本不存在）；Delete(key) 无视一切锁，当前已是标记也照加标记。
12. Get 三态可区分：数据版本返回内容；当前为标记报 ErrDeleted；键无现存版本报 ErrNotExist。
13. 本地验证：go test ./...（含 1500 组随机序列与独立朴素模拟逐步对照）、go test -race、touched 在 100 与 10000 两档均 ≤2 的对照表驱动用例；日志打印输入、输出与判定依据。
