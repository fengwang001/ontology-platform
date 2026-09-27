// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码器。
//
// 单个 Transcoder 实例不是并发安全的：同一时刻只能有一个 goroutine 调用。
package stream

// Encoding 标识输入/输出编码。
type Encoding int

const (
	U8   Encoding = iota // UTF-8
	U16LE                // UTF-16 little-endian
	U16BE                // UTF-16 big-endian
)

// Config 构造转码器。
type Config struct {
	From, To Encoding
	Strict   bool   // true=严格模式；false=替换模式（非法单元→U+FFFD）
	KeepBOM  bool   // true=流首 BOM 作为 U+FEFF 输出；false=丢弃流首 BOM
	Limit    int    // 输出字节上限；<=0 表示不限
	Bias     int    // （par 用）本实例输入首字节的全局偏移
	OddStart bool   // （par 用）UTF-16 段起点为奇数字节
}

// Stats 可读出的统计。
type Stats struct {
	Scalars     int // 已接受的合法标量数（不含作为标量输出的 BOM）
	BadUnits    int // 非法单元数
	BadBytes    int // 被非法单元吞掉的字节数
	BOMBytes    int // 流首 BOM 占用的字节数（0 或 2/3）
	ScalarBytes int // 合法标量占用的输入字节数
}

// Transcoder 流式转码器。
type Transcoder struct{}

// New 按 cfg 构造转码器。
func New(cfg Config) *Transcoder { return &Transcoder{} }
