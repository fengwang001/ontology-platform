// Package ontology 提供进程内存中的两表等值连接（Inner / Left）。
//
// 表是 []map[string]any，每行一个 map。连接按一组有序连接键做等值匹配，
// 语义规则如下：
//
// 空键（NULL 三值语义）：某行的任一连接键属性不存在、为 nil、或为
// float64 NaN 时，该行的键为"空"。空键行在 Inner 下永不匹配任何行
// （包括另一侧同样空键的行，空不等于空）；在 Left 下作为未匹配左行
// 照常输出。Stats.LeftNullKeyRows 单独计数这类左行，与"键有值但右表
// 无对应行"的 Stats.LeftUnmatchedRows 严格分开。
//
// 重复键：同一键上左侧 m 行、右侧 n 行恰好展开为 m*n 个结果行，
// 每对一次、不重不漏。Stats.MatchedPairs 等于逐键 m*n 之和，
// Stats.KeyExpansions 给出逐键明细，Stats.MaxKeyExpansion 是单键
// 最大展开倍数。
//
// 输出顺序：完全确定，与两表输入顺序、map 迭代顺序无关。先按连接键
// 逐列升序（数值 < 字符串 < 布尔，数值按值、字符串按字典序、布尔
// false<true）；同键内按行标识升序，先左行后右行。行标识定义为：
// 把该表全部行按 encodeRow（属性名排序后的内容编码）升序排列后该行
// 的位次；完全相同的行位次相邻，其产出的结果行也相同，故结果序列
// 唯一。Left 下空键左行排在所有非空键分组之后，按行标识升序。
//
// 键比较：连接键只支持 string、bool、int、int64、float64。int64 与
// float64 可比且按数值相等判断；NaN 视为空键永不相等；+0.0 与 -0.0
// 相等。同名键两侧类型不可比较（如 string 对 int64）时返回
// *KeyTypeError，指出键名与两侧类型；不支持的类型返回
// *UnsupportedKeyTypeError。两者都可用 errors.As 判定。
//
// 结果行：连接键与左侧属性保持原名；右侧非连接键属性一律命名为
// "right."+原名（RightPrefix），因此右表同名属性绝不覆盖左表。
// Left 下未匹配左行的右侧属性整体缺失（非零值），用 RightValue 的
// 第二个返回值辨认。结果行的值全部深拷贝，修改结果不影响输入，
// 修改输入也不影响已产出的结果。
package ontology
