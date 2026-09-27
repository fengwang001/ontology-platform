// Package u8 是字节级自写的流式 UTF-8 解码器与编码器。
package u8

// Dec 逐字节解码，不依赖 unicode/utf8。
type Dec struct{}

// Enc 编码标量值为 UTF-8。
type Enc struct{}
