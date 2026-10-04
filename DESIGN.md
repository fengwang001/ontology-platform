# 操作历史保留 + 保留租约 + 副本追赶 设计说明

## 包划分
- `history`：操作序列（Index/Delete，seq 自 1）、单调时钟 now、H（完整下界，初值 1）、存活集合、maxSeq；并持有一把共享 RWMutex，三包共用以保证全局串行化等价。
- `lease`：SetGlobalCheckpoint 与租约集合（name,r,lastRenew）；不自带时钟，所有方法在 history 锁内执行，先查时钟再推进，被拒不推进、不剔除。
- `recovery`：只读 Plan(c)，按 c+1 与 H 比较给出 OpsBased / FileBased。

## 关键取舍
- 过期租约只在 Merge 开头惰性剔除：未剔除的过期租约可被 RenewLease 续活、仍占 Lmax 名额、仍参与 floor；到期即立即失效的方案被放弃，因为它无法复现“过期未剔除可续活”。
- Merge 清除区间 [H, floor) 内只清“被取代者”和 Delete 墓碑，未被取代的存活 Index 留在历史中（即使 seq < 新 H）。放弃“按 H 截断整段历史”：那会丢掉仍存活文档，使 FileBased 无法自举。不变量只保证 seq>=H 不缺失，不要求 seq<H 的内容。
- “被取代”判定用 per-id 最新 seq（map[id]int）做 O(1)，不扫描同 id 历史；Merge 逐槽访问区间内 floor-H 条记录，touched 计数器证明触碰量仅 floor-H，与 maxSeq/存活数无关。
- 拒绝次序严格按非法参数 > 时钟回退 > 租约存在性 > 租约回退 > 历史不可得(r<H) > 超限；Delete 文档不存在、gcp 回退在时钟回退之后。每类错误独立哨兵，errors.Is 可区分；失败路径在任何状态变更前返回。
- 租约 r 的范围统一为 1<=r<=maxSeq+1（非法优先于存在性校验，保证两类操作同一语义）；成功 Add/Renew 后 r>=H，故 floor>=H，H 只增且不超过 gcp+1。
- AddLease 与 Merge 的原子性：lease 方法通过 history 提供的 Lock/Unlock（持锁）方法与 Merge 串行，杜绝“校验 H 后、插入前 H 推进”的窗口；Plan 走 RLock。

## 本地验证
- `go build ./...`；表驱动测试覆盖：过期恰等 E / 大 1、过期未剔除可续活而剔除后不存在、Delete 与被取代清除而存活 Index 保留、gcp 与租约分别决定 floor、c+1 恰等 H 与小 1、r 恰等 H、拒绝次序、被拒不剔除。
- 随机 1500 组操作序列：与逐步朴素模拟逐步对照错误/返回；对每个可达 c 应用 Plan(c) 结果后副本存活集合与主一致；并校验 ops 结果连续无洞、FileBased 存活集 id 字节序及 seq。
- 对照基准：历史 1000 vs 100000 条、同清 10 条区间，断言 touched 都为 10。
- `go test -race ./...` 与 `go vet ./...`。
