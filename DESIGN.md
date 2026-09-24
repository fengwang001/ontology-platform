# DESIGN：结构化日志字段脱敏与一致性采样

## 1. 数据模型（record）
- Record = Level（debug/info/warn/error/fatal）、TraceID（string）、Fields（嵌套 map[string]any，叶值限 string/int64/float64/bool/nil；容器为 map 或 []any）。
- 深度定义：根 map 深度=1，每嵌套一层 +1；上限 MaxDepth=16，超限返回 ErrTooDeep 并计数（遍历时携带 depth 参数，天然无递归失控/栈溢出）。
- 构造期用 `map[uintptr]struct{}` 记录在途 map 指针做循环引用检测，自引用返回 ErrCycle。
- 编码用 encoding/json（确定性：按 key 排序，标准库默认行为），供“非目标字段逐字节不变”比对。

## 2. 路径语法与点号消歧（record/rule）
- 规则路径按 `.` 分段为字面量段；先查整段键名，再按下钻解释，二者冲突时规则如下：
  **消歧规则：字面量整键名优先。** 解析规则串时支持转义：段内 `\.` 表示键名中的字面点，`\\` 表示字面反斜杠。
  即规则 `a\.b` 只匹配“键名就叫 a.b”的字段；规则 `a.b` 在同时存在键 `a.b` 与嵌套 `a→b` 时，优先命中整键 `a.b`；若不存在整键再下钻嵌套。
- 中间层不是 map（是标量/数组）：该路径不匹配，不报错，跳过。
- 路径不存在：不匹配，跳过。
- 数组用段 `[i]`（如 `users.[0].phone`）；越界跳过。

## 3. 规则集与复杂度（rule）
- 规则 = Path + Action(replace/hash/truncate) + 可选 ValueRegexp（按值模式，命中任意字符串叶值即脱敏）+ 参数（替换串/截断 N）。
- 预编译为一棵路径 trie（Node：children map[段]*Node，action）。遍历记录时沿 trie 同步下钻，每条字段边最多触发一次 map 查找；计数器 matchLookups 每次子节点查找 +1。
- 复杂度：lookups ≤ 记录数 × 字段边数 × 1 + 常量，非 记录数×规则数×字段数。
- 矛盾检测：同一路径最终 action 被指定两次且 action 不同（replace 与 hash 等）→ 编译期 ErrConflict，错误串列出冲突两条规则。ValueRegexp 非法同样编译失败。

## 4. 脱敏（mask）
- replace：固定串（默认 `***`），不保留长度。
- hash：`sha256:` + hex(sha256(原串))；同值同结果、不同输入不同结果（抗碰撞假设）、不等于原值且不可逆。
- truncate：保留前 N 个 rune，超出则结果加后缀 `…(truncated)`；未超出不变。
- 路径命中：在对应叶值替换。值模式命中：递归所有字符串叶值（含不同路径、数组元素内）。
- 同一敏感值多处出现：值模式保证逐叶检查，全部处理。
- 非目标字段不变：先序列化快照、脱敏、再序列化比对除命中路径外的 JSON 子树（测试按未命中键集合逐字节比较）。

## 5. 一致性采样（sample）——核心推导
要求：同一追踪链所有记录决策一致；决策不得依赖记录内容、到达顺序、时间。
推导：
1. 每条记录独立随机掷骰 ⇒ 同链各记录独立，链被切碎，否决。
2. 用时间/计数器/到达顺序 ⇒ 重放与乱序结果不同，否决。
3. 唯一满足“仅依赖链标识、确定性、与顺序无关”的输入是 TraceID 本身：
   h = FNV-1a64(TraceID)；keep = h%100 < rate*100（rate∈[0,1]）。
   单次决策恰好 1 次哈希计算（计数器 hashCalls 断言）。
4. error 及以上：无条件保留；若其链的确定性决策为丢，则给记录打标记 IncompleteChain=true（“链不完整”），表示链其余部分被采样丢弃。该标记只在此情形出现。
5. 无 TraceID / 空串：定义为“合成桶”，统一用空串做哈希 ⇒ 同一空桶全体决策一致（rate 决定该桶整体去留）；文档固定此策略，error 规则同样适用。
6. rate=0：全丢（error 仍保留并标不完整）；rate=1：全留。

## 6. 落盘格式与损坏分类（sink）
文件布局：魔数 `ONTLOG1`(7B) + 版本(1B) + 保留标志(2B) = 10B 自描述头；其后每条：
4B 大端长度 L（JSON 记录体）+ L 字节体 + 4B 大端 CRC32-IEEE(体)。
截断点逐字节分类（n 为截断后长度）：
- n<10：ErrHeader（头部不完整）。
- 头后：按帧解析；边界函数 prefix(n)=完整且 CRC 正确的连续帧数。
- 帧 4B 长度前缀未取全：ErrLenPrefix（长度前缀不完整）。
- 长度已读出但体不足：ErrBody（记录体不完整）。
- 体完整但 CRC 4B 缺失：ErrCRC（CRC 不完整/不匹配）。
- 体与 CRC 都在但校验不符：ErrCRC。
恢复记录数 = 最大可恢复前缀（遇到第一个坏帧即停）。四类错误均为哨兵错误，errors.Is 可判；与 ErrConflict/ErrTooDeep/ErrCycle 同套可判定错误。

## 7. 并发
规则集/trie 编译后只读；计数器用 atomic；sampler 无写状态（每次重算哈希）。处理管道对同一输入切片并行处理后按序汇总，输出与串行逐元素一致；-race 干净。

## 8. 包划分（≤9 个 .go 文件）
record/record.go，rule/rule.go，mask/mask.go，sample/sample.go，sink/sink.go，
测试合并：record/record_test.go，rule/rule_test.go（含 mask 断言），sample/sample_test.go（含并发），sink/sink_test.go，cmd/demo/main.go。
每个文件 ≤200 行；表驱动、单测试函数多表行；逐字节/截断点用循环。
