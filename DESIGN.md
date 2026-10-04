# 边缘网关拓扑 / 代理会话管理器 — 设计说明

## 结构与取舍
1. 三包：`topo`（节点/绑定/容量/层级，持有唯一的 `sync.RWMutex`）、`session`（纪元、上线、接管、离线级联）、`route`（下行路径解析）。错误哨兵（`ErrNotFound/ErrType/ErrDepth/ErrFull/ErrNotBound/ErrOffline/ErrStaleEpoch/ErrInvalid/ErrExists/ErrBusy`）统一定义在 `topo`，其余包别名复用。
2. 状态单一数据源：在线标志与纪元只存于 `session.Manager`，拓扑只存名字、kind、parent、children。避免双源不一致；放弃“topo 缓存在线状态”的方案。
3. 并发等价串行：只有 topo 一把大锁，经 `Graph.Lock/Unlock` 导出给 session/route 在整个复合操作（校验+改态+级联）期间持有。放弃细粒度分层锁：操作总量小、级联需同时读拓扑与会话，细锁无法在不放宽不变量的前提下简化推理。
4. 级联事件由 session 产生（它独占纪元）。topo 的 `Bind/Unbind/RemoveNode` 通过构造时注入的钩子 `OfflineHook(name) []Event` 完成“在线节点先级联离线”；钩子在锁内调用。放弃由 topo 直接改会话：那会形成 topo→session 与 session→topo 的循环依赖。
5. 接管语义：节点再次 Online 时，旧会话的整棵在线子树先级联离线（后代先于祖先，兄弟按名字节序），再占用一个全新纪元。放弃“后代过继给新会话/会话沿用旧纪元”：题设要求父纪元刷新即代表旧授权域失效，过继会使旧纪元节点悬挂于新纪元之下，破坏“每个纪元至多一条离线事件”与“节点在父当前纪元下上线”的不变量。
6. 改绑在线节点：先离线级联再改绑，改绑前的所有校验（ErrNotFound>ErrType>ErrDepth>ErrFull；空操作先于 ErrFull）失败则绝不触发级联。放弃“保持会话平移到新父”：节点是在旧父纪元下上线的，平移无法保证父纪元匹配。
7. 校验严格按题面次序短路：非法参数最先；Online 次序 NotFound>ErrNotBound（via 与绑定不符）>父不在线>ErrStaleEpoch；Offline 次序 NotFound>ErrOffline>ErrStaleEpoch。纪元仅在全部校验通过后自增，拒绝操作不占号、不出事件。
8. 事件顺序=后序 DFS，兄弟按 `bytes.Compare`；结果天然确定，重放一致。
9. Route 沿 parent 链向上最多 2 跳（路径≤3），逐跳查当前纪元；未绑定根在线、路径上任一跳离线即 ErrOffline。
10. `touched` 为非导出计数器，同包测试（白盒）读取：级联只在进入在线节点时 +1，故恒等于事件数；Route 每跳 +1，≤3。

## 放弃的其他方案
- 事件总线/异步发布：无法保证“操作返回即事件次序可见”与精确重放。
- 引用计数式会话共享：父重登时旧子树引用旧纪元，违反当前纪元约束。

## 本地验证
`go build ./...`；`go test -race -v ./...`：表驱动样例（题面两例）、错误优先级、两种层级超限、空操作先于 ErrFull、改绑失败不离线、旧纪元 Offline、在线蕴含父在线、touched 两档对照（10000/10 孩子各 3 在线，均为 3）；1500 组固定种子随机操作序列与逐步朴素模型逐操作对照并在失败时打印输入/输出/判定依据；每步后校验路径长度≤3、容量≤Cmax、在线蕴含父在线且父纪元匹配、纪元连续无洞、每纪元至多一条离线事件。
