# 设计说明：浏览上下文跨源隔离与权限策略求值内核

1. 模块划分（`bcontext` 包，4 个 .go 文件）：
2. - `model.go`：六类可区分错误、公开输入类型（Header/FrameAllow/Config/Logger）、内部 doc/edge/context 与配置判定辅助。
3. - `kernel_all.go`：五类变更（LoadTop/LoadFrame/Navigate/SetFrameAllow/OpenPopup）、三类查询（Isolated/FeatureAllowed/OpenerReference）、拒绝次序与日志。
4. - `scenarios_test.go`：规定覆盖面的固定场景矩阵。
5. - `differential_all_test.go`：独立朴素模型 + 随机差分 + 并发 + 复杂度验证。
6. 关键取舍：隔离状态在装载时一次性计算并缓存为 `doc.isolated`，故 Isolated 为 O(1)；导航父文档用“墓碑”（context 保留、doc 置空）区分上下文不存在与文档不存在。
7. 能力求值只沿“被查文档→根”单链上行：根看自身声明/默认；每跳校验自身声明含自身来源、嵌入边允许子来源；隔离门限读缓存标志，故为 O(深度)，与总节点数无关。
8. 拒绝次序硬编码在每个操作内：参数非法→上下文不存在→文档不存在→嵌入策略→开启者断开→能力未知；任何拒绝在状态写入前返回，保证原子。
9. 嵌入边（allow 属性）固定在 frame 上：LoadFrame 随建随定；SetFrameAllow 只置 pendingEdge，下一次装载该 context 时生效，当前文档求值不变。
10. 开启者组与树解耦：popup 仅记 popupOf 与一次性的 openerBroken；断开单向永久，导航不恢复。
11. 被放弃的方案：
12. - 每次查询实时重算隔离：会让 Isolated 随深度增长，违反 O(1)，放弃。
13. - 查询时扫描全树做能力传递闭包：O(节点数)，放弃，改为单链上行。
14. - 父导航物理删除子 context：无法区分“未知 id”与“文档被替换”，改为墓碑。
15. - 细粒度每节点锁：收益不明且易破坏“某串行顺序”的可论证性，改为单把 RWMutex。
16. 并发：写操作 Lock、查询 RLock；互斥写使所有交错等价于某串行序列；`go test -race` 验证。
17. 性能可验证性：BenchmarkIsolated 在 depth=1/10/100/1000 下 ns/op 持平（约 100–170ns）；BenchmarkFeature 随深度线性（约 0.7µs→141µs）；另有“宽树林不影响单链结果”结构测试。
18. 日志：Logger 接口逐操作记录输入、ACCEPT/REJECT、输出与判定依据；nil 时为空实现。
19. 本地验证：
20.   export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
21.   gofmt -l . && go vet ./...
22.   go test -race -v ./...
23.   go test -bench . -benchtime 1000x -run '^$' ./...
24. 差分模型（`naiveModel`）不缓存隔离、每次沿父链重算，与内核对同一随机操作序列（400 种子 × 80 步）逐结果（含错误类别）比对一致。
