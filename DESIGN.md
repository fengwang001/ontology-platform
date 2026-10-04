# 远程执行调度器设计说明

## 包划分
- `action`：纯状态层。`Waiter`（编号、prio、终局 channel）、`Op`（digest、platform、seq、状态 Queued/Assigned、丢失次数、等待者集合）；在途表与结果缓存的索引与 `lookups` 计数。重算有效优先级只遍历该 Op 自己的等待者。
- `worker`：`Worker`（名字、props、槽位总数、已占槽数、持有操作集合）与注册校验，不知道队列与缓存。
- `exec`：`Scheduler`，单把 `sync.Mutex` 串行化全部变更，等价某一线性化顺序；持有 cache map、digest→Op 索引、seq→Op 的有序队列切片、waiter 编号表。

## 关键取舍
- 同摘要合并：Execute 先查 cache（1 次）再查在途索引（1 次），共 ≤2 次 map 查找，与在途操作数无关，由 `lookups` 计数器证明。缓存命中直接 Cached；在途命中则附着；都未命中才入队。skipCache 只跳过缓存，不跳过附着（在途执行本就是新鲜执行）。
- 队列：切片按（有效优先级降序，seq 升序）稳定排序维护；附着升高、取消回落时仅重排该切片。
- 丢失归位：丢失后 seq 保持不变重新排队。放弃“排队尾”方案——排队尾会让反复失联的操作饥饿，也违反“原序号归位”的可复现次序。
- 无人等待的已派发操作：Cancel 使等待者清空时不中止远端执行，转为弃置（orphan）操作继续占槽：exit=0 仍写缓存（结果可惠及后来者），失联则静默删除。放弃“立即中止”方案——无法真正撤销已发出的远端动作，中止只能制造假状态；继续执行且写缓存语义最简单且可复现。弃置期间同摘要 Execute 附着，终局照常分发。
- 终局恰好一次：终局在锁内判定并落定（Result/Cached/Lost/Cancelled），通过容量 1 的 buffered channel 非阻塞投递；终局后的重复 Cancel 报状态不符。
- attempt 号 = 丢失次数 + 1；旧 attempt 的 Complete 与失联后重新 Register 的旧句柄均报尝试过期或状态不符。
- 错误哨兵集中在 `exec` 包（`ErrInvalid`、`ErrNotFound`、`ErrExists`、`ErrState`、`ErrStaleAttempt`、`ErrNoSlot`），支持 `errors.Is`；拒绝次序：参数非法 > 不存在 > 已存在 > 状态不符 > 尝试过期 > 无空槽，先全部校验后改状态。
- `New(M)` 中 M 越界 panic（构造期编程错误）；运行时入参非法返回 `ErrInvalid`。

## 被放弃的其他方案
- 多把细粒度锁：需要跨 cache/inflight/worker 不变量（槽数之和 == Assigned 数、同摘要至多一个在途），单锁正确且可线性化，锁竞争不在目标范围。
- heap 队列：有效优先级随附着/取消频繁变动，维护堆更新复杂；有序切片在规模测试下足够，且次序直观可复现。
- Complete 时遍历全部操作找持有者：改为 Worker 持有集合 O(1) 判定。

## 本地验证
- `go build ./...`；`gofmt -l .` 无输出；`go vet ./...`。
- `go test ./... -race`：表驱动用例覆盖优先级升降、原 seq 归位、跳过不匹配队首、infra 同丢失、恰达 M、弃置完成与丢失、旧 attempt、platform 不一致、skipCache 与缓存覆盖、非 0 不缓存、拒绝次序；lookup 对照在途 100/10000 两档。
- 1500 组固定种子随机序列（-v 打印逐步输入/输出/判定依据），与独立朴素模拟逐步比对等待者终局、缓存、槽位等不变量，失败即打印完整重演日志。
