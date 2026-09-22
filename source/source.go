package source

import "errors"

// ErrShortData 表示数据源在区间读满之前到达 EOF（或声明长度变小），
// 调用方应判定为"读到 EOF 但字节不足"，不得静默截断。
var ErrShortData = errors.New("source: short data: EOF before range satisfied")

// Source 是组装器依赖的字节源抽象。
//
// Size 返回当前字节长度；Length 返回资源在组装开始时的长度。
// ReadAt 语义与 io.ReaderAt 一致：读取 p 的全部长度或返回错误，
// 但实现允许"短读"（n < len(p) 且 err==nil），此时组装器会循环补齐。
// 数据源自身可以用 io.EOF 配合短读来表示数据不足，组装器据此
// 产生 ErrShortData。
type Source interface {
	// Length 返回资源逻辑长度（字节）。
	Length() int64
	// ReadAt 从偏移 off 读取至多 len(p) 字节到 p。
	ReadAt(p []byte, off int64) (n int, err error)
}
