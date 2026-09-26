// Package scalar 判定单个 Unicode 标量值与 UTF-8 首字节约束。
package scalar

// Scalar 是一个 Unicode 标量值（U+0000..U+D7FF 与 U+E000..U+10FFFF）。
type Scalar = rune

// RuneError 是替换字符 U+FFFD。
const RuneError Scalar = '\uFFFD'

// Valid 报告 r 是否为 Unicode 标量值（排除代理区与越界值）。
func Valid(r Scalar) bool { return false }

// HighSurrogate 报告 r 是否为高代理 D800..DBFF。
func HighSurrogate(r rune) bool { return false }

// LowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func LowSurrogate(r rune) bool { return false }

// Pair 把高、低代理还原为标量值。
func Pair(hi, lo rune) Scalar { return RuneError }

// Need 返回 UTF-8 首字节 b 声明的序列长度（1..4）；非法返回 1。
func Need(b byte) int { return 1 }

// LegalLead 报告 b 是否可作非 ASCII 的 UTF-8 首字节（C2..F4，排除非最短形式）。
func LegalLead(b byte) bool { return false }

// SecondOK 报告首字节 lead 之后的第二字节是否落在合法区间（最短/越界约束）。
func SecondOK(lead, second byte) bool { return false }

// Cont 报告 b 是否为续字节 80..BF。
func Cont(b byte) bool { return false }

// Checks 由各包内部嵌入，记录「字节被检查的总次数」。
type Checks struct{ N int }

// Touched 记一次字节检查。
func (c *Checks) Touched() { c.N++ }
