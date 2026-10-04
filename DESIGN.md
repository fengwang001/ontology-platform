# 索引路由器设计说明（slot / shardmap / docstore）

1. 模型：固定 R 个路由槽，槽由 routing 哈希与 (h(id) mod P) 偏移相加后 mod R 得到；分片=槽整除 (R/N)。
   R 永不变，仅 N 变化，因此分裂是把槽区间细化（子分片 s 的父为 s 整除 (N2/N)），文档只在父子间搬动。
2. 放弃一致性哈希/虚拟环：一致性哈希在扩缩容时文档去向依赖环上邻居，无法用整除公式精确复现，也无法保证
   “不跨父移动”；预留固定路由槽后，去向只取决于当前 N，分裂天然无同分片 id 碰撞（旧分片内 id 已唯一）。
3. 放弃分裂时预留空槽方案：要求 N 整除 R 且新 N 整除 R 已足够保证槽边界对齐，无需预留；Split 校验
   N<N2、N|N2、N2|R，Shrink 校验 N2<N、N2|N，另加 P>1 时 N2>P。
4. 收缩撞 id 整体拒绝（报字节序最小的冲突 id），放弃“后写覆盖”：覆盖会丢数据且使 Get 结果依赖写入时序，
   违反重放确定性。先在临时新分片表上重分布，发现冲突即丢弃临时表，状态不变后再解锁。
5. 锁层次：docstore 索引级 sync.Mutex（含分片表与文档 map）→ shardmap 全局 RWMutex。写路径先在 shardmap
   取状态快照（RLock），再锁 docstore 具体索引；Split/Shrink 先持 shardmap 写锁，再调 docstore 重分布
   （内部加 docstore 锁）。锁序单向（shardmap→docstore），无环，可并发且等价于某串行顺序。
6. shardmap 状态（N、R、P、写阻塞）以快照值传出；docstore 依据快照定位，重分布成功后才提交新分片表，
   保证被拒绝操作不改变任何状态。
7. 哈希经 CreateIndex 的可选参数注入，默认 32 位 FNV-1a；加法在 uint64 中进行（h 为 uint32），h 接近
   2^32 也不回绕。P=1 时偏移恒为 0，此时 routing 可缺省（以 id 充当）。
8. Get/Delete 只访问定位到的单个分片，绝不扫描其它分片；docstore 维护非导出 touched 计数
   （Get 触碰分片数、记录检查数），测试断言其与总文档量无关（100 与 100000 两档均为 1/<=1）。
9. SearchShards：P=1 返回单分片；否则枚举 h(routing)+0..P-1（各自 mod R）映射到分片，升序去重，
   自然覆盖跨 R 回绕。
10. 错误为哨兵（ErrInvalidArgument/ErrExists/ErrNotFound/ErrReadOnly/ErrMissingRouting/
    ErrNotSplittable/ErrNotShrinkable/ErrIDConflict），errors.Is 可区分；拒绝次序：
    参数非法 > 不存在/已存在 > 只读或未阻塞 > 缺少路由 > 不可分裂/收缩 > id 冲突 > 文档不存在。
11. 文档按“记住的 routing（含 id 充当情形）+id”重定位；同 id 不同 routing 落不同分片则各自并存，
    同分片同 id 覆盖。分片内 map[id]record 即编码该唯一性。
12. 本地验证：go build ./...；go test ./...（表驱动覆盖各规则与示例）；go test -race ./...；
    随机 1500 组操作序列与逐步朴素模拟逐操作比对返回错误类型、Count、Get 结果与不变量，日志打印输入输出。
