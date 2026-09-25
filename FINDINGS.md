# FINDINGS: 双时间事实存储边界语义核查

以下逐条列出实现行为与注释/文档承诺的不一致。每条给出复现输入、实际输出、
应当输出与根因分析。所有「实际输出」均由 `characterization_test.go` 中的
表驱动测试钉住（测试断言当前真实行为，全部通过）。

## F1. Corrections 把「区间收窄残片」计为一次值修正

- 文档承诺：`Corrections` 返回「修正轨迹：系统曾相信的每个值」
  （query.go 注释 "every value the system ever believed"）。
- 复现输入：`Write(e,p,"old",[10,20),tx=1)`，再 `Write(e,p,"new",[13,16),tx=2)`，
  然后 `Corrections(e,p,validAt=11)`。
- 实际输出：2 个条目 `{old,TxFrom=1,TxTo=2}` 与 `{old,TxFrom=2,TxTo=0}`。
  validAt=11 处的值从未改变（始终是 "old"），真实值变更次数为 0，
  轨迹却有 2 条。条目数与真实修正次数脱节：一次纯区间收窄与一次真实
  值变更（对照组 full-cover 下 `Corrections(11)` 同样返回 2 条）在条目
  计数上无法区分。
- 应当输出：1 个条目（该点信念从未变化），或文档应明确「条目按事实
  而非按值计，区间收窄也算一次 belief 条目」。
- 根因：`Corrections` 收集所有覆盖 validAt 的事实，而 `Write` 为被收窄
  旧事实生成的左/右残片（`residualLeft`/`residualRight`）是与旧事实同值
  的新事实，各自带独立 TxFrom，被当作独立 belief 计入。

## F2. 「strictly increasing TxFrom」只在未声明的不变量下偶然成立

- 文档承诺：`Corrections` 结果「stable, strictly increasing TxFrom」。
- 复现输入：同 F1 的两次写入。
- 实际输出：事实层面上，一次重叠写入产出的左残片、右残片与新事实
  **共享同一 TxFrom**（split-middle 形态下有 3 个事实 TxFrom==tx2），
  严格递增在事实层被违反；但在任一固定 validAt 的 `Corrections` 结果中，
  TxFrom 确实严格递增——因为同一事务产出的事实 valid 区间两两不相交，
  同一 validAt 至多被其中一个覆盖，`sort.SliceStable` 对相等键的稳定
  性从未被实际触发。
- 应当输出：文档承诺成立，但成立原因（同事务事实 valid 不相交这一
  不变量）未被任何注释或测试声明；一旦该不变量被破坏（如未来允许
  同事务产生相交事实），排序结果将退化为依赖插入序。
- 根因：残片的 `TxFrom` 取 `old.TxTo`（即本次 txAt），与新事实的
  `TxFrom = txAt` 相同；`Corrections` 按 TxFrom 排序时对相等键仅保持
  收集顺序（即 facts 切片的 append 顺序）。

## F3. AsOf 的「第一个可见覆盖事实」依赖未声明的唯一性不变量

- 文档承诺：无。`AsOf` 注释未说明多个可见事实覆盖同一点时返回哪一个。
- 复现输入 A（公共 API）：F1 的两次写入后扫描 validAt ∈ [4,26]。
- 实际输出 A：任一 validAt 至多一个可见事实覆盖（可见事实 valid 区间
  两两不相交），因此线性扫描的首个命中是确定的；将 facts 切片整体
  反转后所有 `AsOf` 结果不变。即：通过公共 API 不存在「多个可见事实
  覆盖同一点」的输入。
- 复现输入 B（构造态）：直接向 `es.props["p"]` 放入两个均可见、均覆盖
  validAt=15 的事实 `{a,TxFrom=1}`、`{b,TxFrom=2}`。
- 实际输出 B：`AsOf` 返回切片中**靠前**的那个；交换两者顺序返回值
  随之改变（"a" ↔ "b"），与 TxFrom 先后无关。
- 应当输出：若唯一性不变量属于设计契约，应写入注释/测试；否则 `AsOf`
  应有确定的 tie-break 规则（如最大 TxFrom）。
- 根因：`AsOf` 线性扫描、首个 `visibleAt(txAt)` 命中即返回，无任何
  tie-break；唯一性仅由 `Write` 的拆分逻辑隐式维持。

## F4. Write 不校验 txAt 与 valid 区间的先后：未来知识与任意回填均被接受

- 文档承诺：仅「txAt 必须严格大于该实体上次写入时间」，未提及 txAt
  与 validFrom/validTo 的关系；但「系统在 txAt 知晓 [validFrom,validTo)
  内的事实」的语义暗示 txAt 不应早于 validFrom。
- 复现输入：`Write(e,p,"v",[100,200),tx=1)`（txAt << validFrom，未来
  知识）；`Write(e,p,"v",[1,2),tx=50)`（深度回填）。
- 实际输出：均返回 nil，写入成功且可查询（`AsOf(150,tx=1)` 命中）。
  txAt 等于 validFrom、落在 valid 区间内、晚于 validTo 等形态同样全部
  接受。
- 应当输出：若「未来知识/回填」应被拒，需要新的校验与哨兵错误；
  若应被允许，应在 `Write` 文档中明确「txAt 与 valid 区间无先后约束」。
- 根因：`validateWrite` 只校验 validFrom 非零、区间非空非倒置、txAt
  非零；`Write` 另校验 tx 单调性，但二者都不比较 txAt 与 valid 区间。

## F5. 「写不同实体从不互阻塞」仅在实体级锁成立，查找阶段在全局锁串行

- 文档承诺：`Store` 注释 "writes to different entities never block each
  other"。
- 复现输入：测试内持有 `s.mu`，同时对**已存在**的实体发起
  `Write("a",...)`、`Write("b",...)`、`AsOf("a",...)`。
- 实际输出：三种操作全部在全局互斥锁上阻塞（观测到 ≥100ms 未完成的
  记录，见 `TestEntityLookupSerializesOnGlobalMutex` 日志），释放 `s.mu`
  后才完成。即每次 Write/AsOf 的实体查找阶段都在 `s.mu` 上串行，承诺
  的「从不互阻塞」只在实体级 `es.mu` 阶段成立。
- 应当输出：实体已存在时的查找不应占用全局写锁（如 `sync.RWMutex`
  读路径或 `sync.Map`），或文档应弱化为「写入临界区不互阻塞」。
- 根因：`entity()` 无条件 `s.mu.Lock()`，无论实体是否存在、调用方是
  读还是写，没有读锁快路径。

## 附：不变量小结

- 不变量 I1（可见事实 valid 区间两两不相交）由 `Write` 的关闭+残片
  拆分隐式维持，是 F2 排序承诺与 F3 返回确定性的共同根基，但代码与
  文档均未声明。
- F1–F3 都源于同一设计选择：残片是「同值新事实」而非「旧事实区间
  的视图」，修正轨迹按事实而非按值收集。
