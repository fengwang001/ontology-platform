// Package qpline 提供 Quoted-Printable 的单行级编码判定：
// 哪些字节需要转义、行尾空白处理、软换行插入位置。
package qpline

// MaxLineLen 是一条物理行（不含行尾 CRLF）的最大字符数。
const MaxLineLen = 76

// Builder 按输入字节顺序构造编码输出，维护当前列号与检查计数。
type Builder struct {
	out    []byte
	col    int
	checks int
}

// NewBuilder 返回空 Builder。
func NewBuilder() *Builder { return &Builder{} }

// Checks 返回输入字节被检查的总次数。
func (b *Builder) Checks() int { return b.checks }

// Put 追加一个输入字节的编码结果：esc 为真时输出 =XX，否则字面输出。
// more 表示该逻辑行（当前硬换行之前）后续还有输入字节，用于决定能否占满 76 列。
func (b *Builder) Put(c byte, esc bool, more bool) {}

// HardCRLF 追加一个硬换行并重置列号。
func (b *Builder) HardCRLF() {}

// Bytes 返回已构造的输出。
func (b *Builder) Bytes() []byte { return b.out }

// Escapes 报告字节 c 是否必须写成 =XX（不包含行末空白的上下文判定）。
func Escapes(c byte) bool { return false }
