// Package rle 实现带转义的文本游程编码编解码器。
package rle

// Encode 返回 s 的规范 RLE 编码。
func Encode(s string) string { return "" }

// Decode 严格解码 t；任何非规范或非法输入都返回错误。
func Decode(t string) (string, error) { return "", nil }

// Decoder 是流式严格解码器：解码结果写入构造时给定的 io.Writer。
type Decoder struct {
	checked int64
}

// NewDecoder 创建流式解码器。
func NewDecoder(_ interface{}, _ ...Option) *Decoder { return &Decoder{} }

// Option 配置 Decoder。
type Option func(*Decoder)

// Write 喂入编码字节的任意片段。
func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }

// Close 表示输入结束。
func (d *Decoder) Close() error { return nil }

// BytesChecked 返回输入字节被检查的总次数。
func (d *Decoder) BytesChecked() int64 { return d.checked }
