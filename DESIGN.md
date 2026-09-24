# 结构化日志脱敏与采样器设计

仅标准库；状态驻留进程内存；输出落盘。包：record / rule / mask / sample / sink / cmd/demo。

## 1. 记录模型 (record)
- `Record{Fields map[string]any, TraceID string, Level int}`，level: debug=10 info=20 warn=30 error=40 fatal=50；error 及以上 = >=40。
- `New` 构造时做一次校验：
  - 深度：根映射记深度 1，子映射 +1；超过 `MaxDepth(=32)` 返回 `ErrDepthExceeded`。用显式栈迭代，杜绝深递归栈溢出。
  - 循环引用：自引用（同 map 指针出现在祖先链）返回 `ErrCycle`。用访问标记 + 父指针链迭代检测。
- 编码：`json.Marshal` 信封 `{level,trace_id,fields}`；`Decode` 反序列化回 Record。空记录（nil 字段）合法，序列化为 `{}`。

## 2. 嵌套路径与点号消歧 (rule)
- 路径按 `.` 分层；字面点号用反斜杠转义：`a\.b` 表示键名 "a.b"；`\\` 表示字面反斜杠。
- 解析逐字符扫描，不使用 `strings.Split`。
- **解析（歧义消解）规则（优先级从高到低）：**
  1. 段内存在含点键时，先尝试整段作为“字面键”精确匹配；
  2. 否则按未转义 `.` 拆分为嵌套路径逐层下钻；
  3. 例如记录同时含顶层键 `"a.b"` 与嵌套 `a.b`：输入 `"a.b"` 命中字面键；输入 `"a\\.b"` 亦命中字面键；要命中嵌套只能依赖规则 2（顶层不存在字面键 `a.b` 时下钻）。因此规则书写约定：键名含点时一律写转义形式 `a\.b`，嵌套写未转义 `a.b`，两者互不误命中。
- 下钻任一层不是 map（如标量/数组）→ 不匹配，无错误（静默）；路径不存在 → 不匹配。数组内映射不作为路径段（路径规则不进入数组；数组内值由“值模式规则”覆盖）。

## 3. 规则集与复杂度 (rule)
- `Spec{Path, Pattern string; Action}`；action: replace/hash/truncate(+KeepN)。
- 编译产物 `Set`：路径规则进 trie（节点 keyed by 段名），值规则为编译后的 regexp 列表。
- 匹配方式：遍历记录全部字段时，对每个 map 节点维护“trie 当前节点指针”，随下钻移动。每个字段仅做 1 次 map+指针操作，故每条记录路径匹配次数 = 字段数 + O(1)，与规则数无关；非导出计数器 `pathLookups` 记录之，`SnapshotLookups` 读取。
- 冲突：同一规范化路径同时出现 replace 与 hash（互斥的两种动作）→ 编译期返回 `ErrConflict`（包装两条规则）。truncate 与另两者互斥关系按 replace/hash 对处理；同动作重复允许。
- 值模式：regexp 在任意字符串值上匹配（含数组元素内），命中即对该值套用动作；同值多处出现均被处理。

## 4. 脱敏 (mask)
- 替换：固定串 `***REDACTED***`（长度不保留）。
- 哈希：值的 SHA-256 十六进制；同值同结果、不同值不同结果、不含原值、不可逆。
- 截断：保留前 N 个 rune 并追加固定标注 `…(truncated)`；空串保持空串。
- 遍历全树（map/slice/string），原地重建；非目标值原样拷贝，JSON 序列化后逐字节不变（键序由 map 迭代→json 稳定排序保证，比对在“序列化-脱敏-再序列化”之间进行）。

## 5. 一致性采样推导 (sample)
- 需求：同 trace 所有记录同决策。若每条记录独立随机（`rand`/时间/到达序），同链必出现分歧 → 链断裂，故**决策函数只能依赖 traceID 本身**：`h=FNV-1a-64(traceID)`，保留当 `h % scale < rate*scale`，scale=2^32。与记录内容、时间、顺序、进程无关；单次决策恰好 1 次哈希（计数器断言）。
- rate=0 全丢；rate=1 全留。
- **无追踪 ID（""）策略**：视为“每条记录各自独立的链”——以记录内容指纹作为回退键仍会让同源空 ID 记录耦合，不符合语义；故空 ID 不参与一致性哈希：rate=1 全留，rate<1 时按 rate 独立概率决定（error 仍强制保留）。文档固定此策略。
- error(>=40) 无条件保留。若其链在 rate 下被采样丢弃，则该记录被打上 `Incomplete=true`（链不完整）；链本就保留或无分歧时不打标。即标记仅出现在“链被丢 + 记录被强留”的交集。

## 6. 落盘格式与损坏检测 (sink)
- 文件 = 头 + 帧序列。头：magic(`ONTLOG01` 8B)+flags(1B)，共 9B。
- 帧：`[len uint32 BE][body][crc32(IEEE) uint32 BE]`；body 为 record 的 JSON 信封字节。
- 顺序读取，截断分类（errors.Is 可判）：
  - `ErrHeaderTruncated`：< 9B 或 magic 错（头部不完整）。
  - `ErrLenTruncated`：len 字段不足 4B。
  - `ErrBodyTruncated`：body 不足 len 字节。
  - `ErrCRC`：crc 字段不足 4B 或 CRC 不匹配。
- `ReadAll` 返回截断前完整恢复的记录 + 首个错误；最大可恢复前缀 = 全部完整且 CRC 正确的帧。

## 7. 四类可判定错误
- `record.ErrDepthExceeded` / `record.ErrCycle` / `rule.ErrConflict` / `sink.ErrHeaderTruncated|ErrLenTruncated|ErrBodyTruncated|ErrCRC`，均哨兵错误，`errors.Is` 区分。

## 8. 并发
- 决策只读 traceID，trie 只读；计数器 atomic；无共享可变状态。多 goroutine 处理互不相交 Record，结果集合（按稳定序）与串行逐元素相同；`-race` 干净。
