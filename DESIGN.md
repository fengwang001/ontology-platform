# 边缘网关/子设备 拓扑与代理会话 — 设计说明

1. 三个包各司其职：`topo` 只维护节点、类型、父子绑定与容量/层级约束；`session` 维护在线集、纪元与离线级联；`route` 只读解析根到节点的下行路径。
2. 并发取舍：状态单一且一致性约束跨包（在线⇔绑定⇔纪元），放弃细粒度分锁（顺序倒置会破坏“在线必父在线”），改用 `topo.Topo` 内一把 `sync.RWMutex`；session/route 复用此锁，对外方法串行化，Route 用 RLock。结果天然等价于某串行顺序，无死锁（无嵌套第二把锁）。
3. 调用方向：session 不 import topo 的绑定变更，改为 topo 暴露 `*Locked` 原语；新建根包 facade（`manager.go`，包名 ontology）按规定的判定次序编排 topo+session，Bind/Unbind/RemoveNode 前由 session 做级联。放弃 session 反向回调 topo hook（会造成 import 环与隐式控制流）。
4. 改绑在线节点：先离线级联再改绑，放弃“保持会话迁移到新父”——会话纪元证明的是“在某父纪元下上线”，父变了旧凭证即失效，保持会话会使 Route 与纪元语义不可复现。
5. 接管：新 Online 先对旧会话整棵子树离线再占新号。放弃“后代过继给新会话”——后代是在旧纪元下上线的，过继违反“在父当前纪元下上线”，且无法定义被过继节点的纪元；级联后显式重新上线语义唯一。
6. 离线级联按节点名字节序（Go string 即字节序）对**在线**直属孩子递归，事件为严格后序 DFS；离线孩子不遍历、不计 touched。
7. 纪元：单个 int64 计数器，仅校验通过且成功上线后 `++` 赋值，保证全局连续无洞、每个只用一次；离线事件携带原纪元，配合“一号一上线”即一纪元至多一条离线事件。
8. 错误次序严格实现：ErrInvalid > ErrExists/ErrNotFound > ErrNotBound/ErrType > ErrOffline/ErrDepth > ErrFull > ErrStaleEpoch，按题目各 API 给定次序逐行排列；任何错误在首次状态修改前返回。
9. Bind 的“已绑同一父”空操作检查先于容量检查，且不触发任何离线与事件。
10. touched 证据：`session.CascadeTouched()` 记录最近一次顶层级联实际遍历的节点数（必等于事件数）；`route.RouteTouched()` 记录最近一次 Route 走过的节点数（≤3）。
11. 数据结构：node 存 parent 与 children（map+按需排序）；在线表 map[string]int64。层级至多两层，路径长度至多 3，由 Bind 的 ErrDepth 两条规则共同保证。
12. 本地验证：`go test ./... -race -count=1`；表驱动用例覆盖全部判定次序与题给示例；另用独立朴素模型对 1500 组随机操作序列逐步比对错误、纪元、事件与路径，`-v` 打印每组输入/输出/判定依据。
