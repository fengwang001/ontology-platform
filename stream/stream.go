// Package stream 实现带非法字节替换的流式 UTF-8⇄UTF-16 转码。
//
// 单个 Transcoder 不是并发安全的。
package stream

import "ontology/u16"

// Direction 是转码方向。
type Direction int

const (
	// U8ToU16：UTF-8 输入；U16ToU8：UTF-16 输入。
	U8ToU16 Direction = iota
	U16ToU8
)

// Config 配置转码器。
type Config struct {
	Dir       Direction
	Order     u16.Order
	Strict    bool
	KeepBOM   bool
	MaxOutput int // 输出字节上限，0 表示不限
}

// Stats 是可读出的统计。
type Stats struct {
	Valid    int64
	Bad      int64
	BadBytes int64
	BOMBytes int64
	Consumed int64
	Checks   int64
}

// Transcoder 是流式转码器。
type Transcoder struct{}

// New 按 cfg 创建转码器。
func New(cfg Config) *Transcoder { return &Transcoder{} }

// Write 喂入输入字节。
func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

// Close 结束流，处理残留半截字符。
func (t *Transcoder) Close() error { return nil }

// Output 返回已累积的输出。
func (t *Transcoder) Output() []byte { return nil }

// Stats 返回当前统计快照。
func (t *Transcoder) Stats() Stats { return Stats{} }
