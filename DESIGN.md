# 设计说明：按路由槽定位、在线分裂与收缩的索引路由器

## 结构
- `slot`：纯函数。`HashFunc`（默认 32 位 FNV-1a）、`Slot(hid,hr,N,R,P)`、`Shard(slot,N,R)`、`SearchSlots`。
- `shardmap`：索引元数据（N/R/P/hash/写阻塞）与合法性判定；包级注册表 `CreateIndex/Get/Drop/SetWriteBlock`，`Index.Resize(N2)` 完成校验并原地改 N。
- `docstore`：包级注册表持有 `map[index]*store`；每分片 `map[string]doc`，doc 记住写入 routing；Put/Get/Delete/Split/Shrink/Count/SearchShards/Touched。

## 定位取舍
- 固定 R 个路由槽、R/N 槽/分片：分裂要求 N|N2 且 N2|R，新分片 s 只来自旧 s/(N2/N)，文档不跨父、同父内旧 shard 内 (id 唯一) 天然无碰撞。放弃一致性哈希：后者会跨父搬迁且需虚拟节点，无法精确复现父子关系。
- slot=(h(routing)+h(id) mod P) mod R：和在 mod R 之前用 uint64 完成，h 接近 2^32 也不回绕；P=1 偏移恒 0。
- routing 为空以 id 充当（写入时记住的是有效 routing）；但 P>1 的写/查均强制显式 routing，报 ErrMissingRouting。
- SearchShards 枚举 P 个槽（含跨 R 回绕）映射分片后升序去重；P=1 恰一个。

## 分裂与收缩
- 两操作都必须先置写阻塞；重分布在锁内基于「记住的 routing/id」按新 N 重算，构建新分片表后整体替换，失败不改任何状态。
- 分裂按父子关系搬移即可；收缩先全量预演，若两条同 id 不同 routing 并入同一新分片则报 ErrIDConflict（取字节序最小 id），不提交。放弃「后写覆盖」：静默丢数据且不可复现谁赢。

## 并发与拒绝次序
- 索引项用各自 mutex；注册表用独立 mutex，命名查重与登记在同一临界区。所有读写经同一把索引锁，等价于某串行顺序。
- 错误次序：参数非法 > 不存在/已存在 > 只读或未阻塞 > 缺少路由 > 不可分裂/收缩 > id 冲突 > 文档不存在；被拒绝操作零状态变更。

## 验证
- 表驱动：回绕、近 2^32 加法、不跨父、三条不可分裂、收缩 id 冲突整体回滚、同 id 并存与 Get 不跨片、缺 routing、写阻塞双向拒绝。
- 1500 组随机序列与独立朴素模拟逐步对照（小值域注入哈希制造碰撞），打印输入/输出/判定；touched 对照 100 与 100000 条；`go test -race` 并发用例。
