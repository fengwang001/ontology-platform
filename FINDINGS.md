# FINDINGS: 劈开残差对 Corrections 轨迹的污染

审计对象：`Write` 的劈开逻辑（store.go `residualLeft`/`residualRight`）与
`Corrections` 的轨迹生成（query.go）。以下每条给出复现输入、实际输出、
应当输出、根因分析。所有「实际输出」均由
`corrections_characterization_test.go` 中的表驱动测试钉住并通过。

## F1. 残差区域出现「幻影修正」：相邻条目值相同，仅事务边界不同

- **文档承诺**：`Corrections` 返回「every value the system ever believed
  for that point」（query.go 注释）；`Correction` 表示「which value the
  system believed, and during which transaction interval it believed it」
  （fact.go）。语义上，轨迹中每出现一个新条目，应当对应一次真实的
  「系统改信了另一个值」。
- **复现输入**：
  `Write(e,p,"old",[10,20),tx=1)`；`Write(e,p,"new",[13,16),tx=2)`；
  `Corrections(e,p,validAt=11)`（落在左残差 `[10,13)` 内）。
- **实际输出**：两条条目 —
  `{old, TxFrom=1, TxTo=2}`、`{old, TxFrom=2, TxTo=0}`。
  值从未改变，轨迹却多出一条「修正」；下游审计会把 tx=2 误判为一次
  值变更事件。三种劈开形态（左覆盖/右覆盖/中间覆盖）均如此。
- **应当输出**：单条 `{old, TxFrom=1, TxTo=0}`——该点的信念自 tx=1
  起连续未变；或至少在生成轨迹时把相邻同值条目合并。
- **根因**：劈开把残差存为一条**全新 fact**（`Value` 照抄、
  `TxFrom = 关闭时刻`），而 `Corrections` 只是把所有覆盖 validAt 的
  fact 原样倒出并按 `TxFrom` 排序，不做同值合并。残差是内部存储
  产物，却被当成了认识史的一部分暴露出去。

## F2. 残差的 TxFrom 是劈开（关闭）时刻，不是原始写入时刻

- **文档承诺**：`Fact` 注释称系统「knew this during the transaction
  interval [TxFrom, TxTo)」（fact.go）；`Correction.TxFrom` 即系统开始
  相信该值的事务时刻。
- **复现输入**：同 F1；检查残差 fact `[10,13)` 的 `TxFrom`。
- **实际输出**：残差 `TxFrom = 2`（劈开时刻）。轨迹声称系统从 tx=2
  才相信「`[10,13)` 上值为 old」，但事实上自 tx=1 起就连续相信。
- **应当输出**：若 `TxFrom` 语义是「系统何时开始相信」，残差应继承
  原 fact 的 `TxFrom = 1`（原始写入时刻）。
- **根因**：`Write` 先执行 `old.TxTo = txAt`（store.go），随后
  `residualLeft`/`residualRight` 以 `TxFrom: old.TxTo` 取到的正是刚
  写入的关闭时刻。注意这依赖「先关闭、后切残差」的语句顺序：顺序
  一旦对调，残差 `TxFrom` 会变成零值（+inf 语义），属于隐式耦合，
  注释（"Boundaries are copied exactly"）未提示这一点。
- **补充**：`Write` 的文档注释「residual facts known from txAt」与
  实现自洽，但与 `Fact`/`Correction` 的「相信区间」语义互相矛盾——
  两份注释对 `TxFrom` 的承诺不一致。

## F3. 轨迹长度随无关写入无限膨胀，审计噪音放大

- **复现输入**：
  `Write(e,p,"v",[10,20),tx=1)`；`Write(e,p,"w",[15,25),tx=2)`；
  `Write(e,p,"x",[5,12),tx=3)`；`Corrections(e,p,validAt=13)`。
- **实际输出**：三条条目 `{v,1,2}`、`{v,2,3}`、`{v,3,0}`——validAt=13
  的值自始至终是 "v"，两次劈开都是发生在别处的写入，却各贡献一条
  「修正」。每多一次无关重叠写，该点轨迹就长一条。
- **应当输出**：单条 `{v,1,0}`。
- **根因**：同 F1——残差级联（残差的残差）逐次关闭/重建，
  `Corrections` 不去重。这是 F1 的放大效应，说明污染不是常数级，
  而是随写入次数线性增长。

## F4. 无限区间劈开同样产生幻影条目

- **复现输入**：`Write(e,p,"old",[10,+inf),tx=1)`；
  `Write(e,p,"new",[15,20),tx=2)`；`Corrections(e,p,validAt=100)`。
- **实际输出**：`{old,1,2}`、`{old,2,0}`，与有限区间情形一致。
- **应当输出**：单条 `{old,1,0}`。
- **根因**：同 F1/F2，`residualRight` 对 `ValidTo` 为零（+inf）的旧
  fact 同样以 `TxFrom = old.TxTo` 生成残差。

## 共同根因总结

残差是 `Write` 为维持「可见 fact 的 valid 区间互不相交」而引入的
**存储层内部结构**，但 `Corrections` 没有区分「用户写入产生的认识
事件」与「劈开产生的存储残片」，把两者混在同一条轨迹里返回。修复
方向（不在本次范围内）可以是：生成轨迹时合并相邻同值条目，或让
残差继承原 fact 的 `TxFrom` 并在查询侧按 `(Value, 连续 Tx 区间)`
折叠。

## 测试落点

- `corrections_characterization_test.go`：
  `TestCorrectionsBaselineSingleWrite`（基线）、
  `TestCorrectionsAfterSplitShapes`（三种劈开形态，循环遍历）、
  `TestCorrectionsAdjacentSameValueEntries`（F1 的相邻同值条目）、
  `TestCorrectionsChainedSplitsAccumulate`（F3）、
  `TestCorrectionsResidualOfInfiniteInterval`（F4）。
  全部断言当前真实行为，均通过。
