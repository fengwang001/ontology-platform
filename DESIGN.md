# DESIGN：结构化日志脱敏与一致性采样

模块 ontology，仅标准库；状态在进程内存，sink 落盘。包：record/rule/mask/sample/sink + cmd/demo。

## 1. 数据模型（record）
- 字段值统一为 `Value`：`map[string]any`（映射）、`[]any`（数组）、`string/int64/float64/bool/nil`。
- `Record{Level, TraceID string; Fields map[string]any}`；Level 取值 debug/info/warn/error，error 及以上为强制保留级。
- 编解码：map 按 key 排序后 JSON 编码（确定性），数组保序；用于逐字节比对。
- 深度上限 `MaxDepth=32`：根映射为深度 1，嵌套逐层计数，超限返回 `ErrDepthExceeded` 并计 `DepthRejected`，拒绝处理。
- 构造时用地址集合做一次 DFS 环检测：同一映射/slice 指针二次出现即 `ErrCycle`（不得递归处理）。

## 2. 路径语法与点号消歧（核心）
- 规则路径形如 `a.b.c`。解析为段序列后做树下降：映射按键精确查找；数组按下标（段为非负整数）透明下降；
  路径中段不是映射/数组、越界、键缺失均视为“路径不存在”，该规则对此记录不生效（非错误）。
- 字段名本身含点（键就是 `a.b`）与嵌套 `a -> b` 存在歧义。消歧规则（确定性、固定优先级）：
  1. 先尝试整段全名精确匹配：若当前映射存在键等于完整剩余路径（如 `a.b.c`），命中该键；
  2. 否则再按点拆分为嵌套路径逐段下降。
  即“含点字面键优先于嵌套解释”，二者只可能命中其一，结果不依赖规则顺序。测试覆盖三种情形。

## 3. 规则与编译（rule）
- 规则：`{Path, Kind, Action, Arg}`，Kind=path 时仅命中该路径；Kind=value 时按精确字符串值全树匹配（数组元素、任意深度、被引用处都算）。
  Action ∈ replace(Arg=固定串，长度不保留)/hash(Arg 忽略)/truncate(Arg=保留字符数 N，追加固定标记 `"…"`）。
- 编译 `Compile`：path 规则放入以首段为键的索引表（map），value 规则放入以“值”为键的表；
  匹配时记录树每下降一个字段，仅查对应桶，因此每条记录路径匹配次数 = O(字段数 + 命中数)，与规则总数无关。
- 矛盾检测：同一路径不得配置两个不同 Action（replace+hash 等）；编译期返回包装了两条规则的 `ErrConflict`（errors.Is 可判定）。
- 计数器 `matchCount`（非导出）记录路径比较次数，供复杂度测试断言上界 `记录数*(字段数+C)`。

## 4. 一致性采样（sample，核心推导）
- 错误做法：对每条记录独立随机掷骰——同一 TraceID 的记录会部分保留部分丢弃，链断裂。
- 正确做法：决策只能是 TraceID 的确定性函数。用 `fnv.New32a()` 对 TraceID 哈希一次，`h % 10000 < rate*10000` 决定保留；
  与记录内容、到达顺序、时间均无关，故同链必同决策，重复运行结果相同。单次决策恰好 1 次哈希（非导出 hashCount 计数）。
- rate=0：全丢；rate=1：全留；rate∈[0,1]。无 TraceID（空串）策略：视为独立单条链，按同一确定性哈希决策
  （空串也有固定哈希值）；但 error 记录仍强制保留。文档固定此语义并测试。
- error 及以上：无条件保留。若其链本应被采样丢弃，则该记录置 `IncompleteChain=true` 输出标记；
  链被保留的 error 不打标记；非 error 绝不打标记。

## 5. 脱敏执行（mask）
- 对记录做单次带环检测的遍历：path 规则按第 2 节路径命中，value 规则按值命中，同值出现在多路径/数组内全部处理。
- 不变量：replace → 固定串；hash → SHA-256 十六进制摘要，同值同结果、不同值不同结果、不等于原值（不可逆）；
  truncate → 前 N 个 rune + `…`，N≤0 时仅标记。
- 非目标字段逐字节不变（以排序 JSON 前后比对验证）。

## 6. 落盘格式与损坏分类（sink）
- 文件 = 头部 `ONTL1\n`(6B) + 若干帧；帧 = 4B 大端长度 + JSON 记录体 + 4B 大端 CRC32(IEEE)。
- 读取逐帧恢复；截断/损坏按前缀位置分类，四类错误均可 errors.Is：
  `ErrHeader`（<6B 或魔数不符）、`ErrLenPrefix`（长度不足 4B）、`ErrBody`（体不足或越界）、`ErrCRC`（CRC 不符）。
- 任意截断点恢复结果 = 该点之前的最大完整帧前缀；逐字节截断用循环覆盖全部切点，不展开用例。
- 并发：Processor 用固定采样器（无状态）+ sink 写互斥；同输入并发输出集合与串行逐元素一致。

## 7. 错误体系
ErrDepthExceeded / ErrCycle / ErrConflict / ErrHeader / ErrLenPrefix / ErrBody / ErrCRC 均为哨兵，
冲突与截断错误用 `%w` 包装，调用方 errors.Is 判定。
