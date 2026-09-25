# Characterization Findings

钉住当前真实行为的测试在 `characterization_test.go`（全部通过）。以下逐条对照
`store.go` / `query.go` / `fact.go` 的注释承诺与实际行为。时间用秒表示，均为
`ts(n)`；区间左闭右开，零 `to` 为 +∞。

## 1. 区间收窄被算作一次「值修正」

- 复现：`Write("e","p","old",10,20,tx=1)`；`Write("e","p","new",13,16,tx=2)`；
  `Corrections("e","p",validAt=11)`。
- 实际：返回 2 条，值依次为 `old`(TxFrom=1,TxTo=2)、`old`(TxFrom=2,TxTo=∞)。
  条目数（2）多于该点真实值变更次数（0，始终是 old）。四种 carve 形态
  （中间/左/右/无限旧区间）均如此。
- 应当：修正轨迹应只在「系统相信的值」发生变化时新增条目；同值残片只是
  valid 区间被收窄，应与旧事实合并或过滤，轨迹长度等于不同值的个数（1）。
- 根因：`store.go:residualLeft/residualRight` 复制 `old.Value` 造出残片，
  `query.go:Corrections` 对所有覆盖该点的 Fact 无差别 append，只按 TxFrom
  排序，不按 Value 去重或合并相邻同值条目。

## 2. 一次写入产出多个相同 TxFrom；「strictly increasing」靠数据布局侥幸成立

- 复现：同上中间 carve。存储中 `new` 与左/右残片共 3 个 Fact，TxFrom 全部=2；
  单侧 carve 时为 2 个。
- 实际：`sort.SliceStable` 对相等 TxFrom 保持原切片顺序，本身不保证严格。
  对单一 `validAt` 的 Corrections 输出恰好严格递增——因为新事实区间
  [13,16) 与残片 [10,13)/[16,20) 在 valid 轴上不相交，同一 validAt 永远不会
  同时命中它们。探针（直接放入两个 TxFrom 相同、都覆盖同一点的 Fact）证实
  排序后两条 TxFrom 相等、先后完全由 append 顺序决定。
- 应当：要么注释改为「按 TxFrom 非递减，同批次顺序未定义」，要么同批次赋予
  可区分的次序（或在收集时保证严格递增并定义 tie-break）。
- 根因：`store.go:Write` 中残片 `TxFrom: old.TxTo` 与新事实 `TxFrom: txAt`
  被设成同一时刻；`query.go` 用 `sort.SliceStable` + `Before` 比较器，对
  tie 无二级排序键。严格性只来自「当前可见事实始终互不重叠」这一结构
  不变量（tx 回归检查 + 残片几何共同维持），而非排序层的保证。

## 3. AsOf 取线性扫描中第一个可见事实，语义依赖切片序（当前不可达）

- 复现：直接构造两个都覆盖 validAt=10、TxFrom=1、TxTo=0 的可见 Fact，仅
  append 顺序不同（a,b 与 b,a）；`AsOf(...,"p",10,tx=9)`。
- 实际：分别返回 `a` 和 `b`——返回值就是 facts 切片里第一个 visible 覆盖者。
  对任何经 Write 产生的历史，遍历全部 (carve 形态, validAt, txAt) 并反转
  内部切片，AsOf 结果不变：每点至多一个可见事实（同一结构不变量），所以
  切片序依赖目前是潜伏的，无公开 API 输入可触发。
- 应当：文档应说明返回值在多可见事实并存时的选择规则（如最新 TxFrom），
  或扫描时做确定性择优；当前注释未承诺任何规则。
- 根因：`query.go:AsOf` 命中第一个 `visibleAt(txAt)` 的覆盖事实即 return，
  无 tie-break；不变量没有在代码中被强制，只是 Write 逻辑的间接结果。

## 4. txAt 与 valid 区间的关系完全不校验

- 复现：未来知识 `Write(...,"v",100,200,tx=1)` 被接受，且
  `AsOf(validAt=150,tx=1)` 立即可见（系统在生效前 99 秒就「知道」未来值）。
  回填 `Write(...,"v",1,10,tx=100)` 同样接受，`AsOf(5,tx=50)` 返回
  ErrNotYetKnown、`AsOf(5,tx=100)` 可见。
- 实际：两类写入均无错误；合法区间 [100,200) 在 tx=1 时即可被读到。
- 应当（若业务禁止预知）：拒绝 `txAt < validFrom`，或文档明确「未来知识/
  回填均为合法双时间语义」。现在注释只字未提，行为未被承诺也未被钉住。
- 根因：`store.go:validateWrite` 只查 validFrom 零值、validTo=validFrom、
  validTo<validFrom、txAt 零值，没有任何 `txAt` 对 `validFrom/validTo`
  的比较；`visibleAt` 只看事务轴。

## 5. 实体查找先走全局 s.mu，不同实体的读写仍被串行化

- 复现：预先建好实体 a、b；测试持有 `s.mu` 50ms，另一 goroutine 对 b
  执行 Write / AsOf / Corrections（b 的实体锁此刻空闲）。
- 实际：三种操作在 s.mu 持有期间全部无法完成，释放后才完成（write/asof/
  corrections 三个子用例一致）。
- 应当：按 Store 注释「writes to different entities never block each other」，
  已存在实体应直接走实体级 `es.mu`，不经过全局锁；实体创建可用
  `sync.Map` 或 RLock + double-check。
- 根因：`store.go:entity` 无条件 `s.mu.Lock()`，Write/AsOf/Corrections
  都先经它再取 `es.mu`；实体级细粒度锁只在第二阶段成立，查找阶段所有
  实体（含读操作）在全局互斥锁上串行。

## 备注

- 未改动任何实现文件；以上 2、3 两条的「违反」状态通过包内直接构造 Fact
  观察，公开 Write API 在 tx 严格递增前提下维持了掩盖问题的不变量。
- 全部 characterization 测试断言当前真实行为并通过：`go test -race ./...`。
