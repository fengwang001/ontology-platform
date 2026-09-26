// Package stream 提供带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 *Transcoder 不是并发安全的；并发场景请用 par 包或每 goroutine 一个实例。
package stream

import "ontology/scalar"

// Encoding 选择输入/输出编码。
type Encoding uint8

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

// Stats 是字节守恒统计。
type Stats struct {
	Scalars  int // 已接受的合法标量数（含被保留的 BOM）
	BadUnits int // 非法单元数
	BadBytes int // 被非法单元（含截断）吞掉的字节数
	BOMBytes int // 被识别并处理的 BOM 字节数
	Consumed int // 已落定的输入总字节数
	Checks   int // 字节被检查的总次数
}

// Config 配置转码器。
type Config struct {
	From, To Encoding
	Strict   bool // 严格模式
	MaxOut   int  // 输出字节上限；<=0 表示不限
	KeepBOM  bool // 流开头 BOM 是否作为 U+FEFF 输出
}

// Transcoder 是有状态转码器。
type Transcoder struct {
	cfg Config
	out []byte
	err error
	closed bool
	stats Stats
}

// New 构造转码器。
func New(cfg Config) *Transcoder { return &Transcoder{cfg: cfg} }

// Write 喂入字节；终态后返回同一个错误。
func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

// Close 结束流；替换模式 flush 残留为 FFFD，严格模式返回截断错误。
func (t *Transcoder) Close() error { return nil }

// Output 返回已累积输出。
func (t *Transcoder) Output() []byte { return t.out }

// Pending 返回未落定的缓存字节（上限续传时与返回数配合使用）。
func (t *Transcoder) Pending() []byte { return nil }

// Stats 返回统计快照。
func (t *Transcoder) Stats() Stats { return t.stats }

// RuneError 重导出替换字符，便于外部使用。
const RuneError = scalar.RuneError
