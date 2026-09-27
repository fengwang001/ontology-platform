// Package u16 是字节级自写的流式 UTF-16LE/BE 解码器与编码器。
package u16

// Dec 逐字节解码 UTF-16。
type Dec struct{}

// Enc 编码标量值为 UTF-16。
type Enc struct{}
