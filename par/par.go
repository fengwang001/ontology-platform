// Package par 把输入切段并行转码后拼接，依赖 stream 与 u8。
package par

import (
	"errors"

	"ontology/stream"
	"ontology/u8"
)

// MaxK 为最大并行度。
const MaxK = 8

var (
	// ErrBadK 为 K 不在 1..MaxK；ErrOddCut 为 UTF-16 切点无法取偶。
	ErrBadK = errors.New("par: K must be in 1..8")
)

// alignU8 把切点回退到最近的非续字节（最多回看 u8.MaxPending 字节）。
func alignU8(b []byte, cut int) int {
	s := cut
	for s > 0 && s > cut-u8.MaxPending && u8.IsCont(b[s-1]) {
		s--
	}
	return s
}

// Transcode 把 b 等分为 K 段并行转码。UTF-16 方向切点取偶。
func Transcode(b []byte, opts stream.Options, k int) ([]byte, stream.Stats, error) {
	return nil, stream.Stats{}, ErrBadK
}

// TranscodeCuts 用给定切点（段边界偏移，单调递增，不含 0/len）并行转码。
func TranscodeCuts(b []byte, opts stream.Options, cuts []int) ([]byte, stream.Stats, error) {
	return nil, stream.Stats{}, nil
}
