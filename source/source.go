// Package source 定义字节源抽象。实现可以短读、出错、在两次调用之间
// 改变长度；本包不依赖工程内其他包。
package source

import "io"

// Source 是一个可随机访问的字节源。Len 返回当前长度（可随时间变化）；
// ReadAt 允许短读（返回 n < len(p) 且 err == nil），调用方必须循环补齐。
type Source interface {
	Len() int64
	ReadAt(p []byte, off int64) (int, error)
}

type bytesSource struct{ b []byte }

// Bytes 返回一个内存字节源。
func Bytes(b []byte) Source { return bytesSource{b} }

func (s bytesSource) Len() int64 { return int64(len(s.b)) }

func (s bytesSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if off >= int64(len(s.b)) {
		return 0, io.EOF
	}
	n := copy(p, s.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Scripted 是可编程字节源，用于注入短读、读错误与长度变化。
type Scripted struct {
	data     []byte
	length   int64
	maxChunk int
	err      error
}

// NewScripted 以 data 为内容创建可编程字节源。
func NewScripted(data []byte) *Scripted {
	return &Scripted{data: data, length: int64(len(data))}
}

// SetMaxChunk 限制单次 ReadAt 最多返回的字节数（<=0 表示不限制），模拟短读。
func (s *Scripted) SetMaxChunk(n int) { s.maxChunk = n }

// SetErr 让后续所有 ReadAt 返回该错误（nil 恢复正常），模拟读错误。
func (s *Scripted) SetErr(err error) { s.err = err }

// SetLen 改变对外可见长度（不改动底层数据），模拟读到一半长度变化。
func (s *Scripted) SetLen(n int64) { s.length = n }

func (s *Scripted) Len() int64 { return s.length }

func (s *Scripted) ReadAt(p []byte, off int64) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.maxChunk > 0 && len(p) > s.maxChunk {
		p = p[:s.maxChunk]
	}
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if off >= s.length {
		return 0, io.EOF
	}
	if remain := s.length - off; remain < int64(len(p)) {
		p = p[:remain]
	}
	n := copy(p, s.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
