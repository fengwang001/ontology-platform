// Package codec 定义单条 WAL 记录的自描述编码：
//
//	[4 字节大端长度前缀][负载][4 字节大端 CRC32 校验和]
//
// 解码只依赖字节流本身，可区分三种结果：完整记录、半条记录、
// 校验和不符。codec 不依赖工程内任何其他包。
package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	// LenSize 是长度前缀的字节数。
	LenSize = 4
	// SumSize 是校验和的字节数。
	SumSize = 4
	// HeaderSize 是长度前缀与校验和的总字节数（不含负载）。
	HeaderSize = LenSize + SumSize
	// MaxPayload 是单条记录允许的最大负载字节数，
	// 用于防御长度前缀声称的超大长度。
	MaxPayload = 1 << 30
)

// Kind 表示一次解码尝试的结果类别，三者彼此可判定。
type Kind int

const (
	// Complete 表示解出一条完整且校验通过的记录。
	Complete Kind = iota
	// Truncated 表示字节流在此处截断，是一条半记录。
	Truncated
	// Corrupt 表示记录字节齐全但校验和不符。
	Corrupt
)

func (k Kind) String() string {
	switch k {
	case Complete:
		return "complete"
	case Truncated:
		return "truncated"
	case Corrupt:
		return "corrupt"
	}
	return "unknown"
}

// Result 是一次解码尝试的结果。
type Result struct {
	Kind    Kind   // 结果类别
	Payload []byte // 仅 Kind == Complete 时有效，为负载的副本
	Size    int    // 仅 Kind == Complete 时有效，为整条记录占用的字节数
	// Missing 仅 Kind == Truncated 时有效：距完整还差的最少字节数；
	// 连长度前缀都不完整时为 -1（总量未知）。用它可区分
	// "只有零长度前缀"与"前缀声称的长度远超剩余字节"等退化输入。
	Missing int
}

// ErrPayloadTooLarge 表示待编码的负载超过 MaxPayload。
var ErrPayloadTooLarge = errors.New("codec: payload exceeds MaxPayload")

// Encode 把负载编码成一条自描述记录，返回新分配的切片。
func Encode(payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, fmt.Errorf("%w: %d", ErrPayloadTooLarge, len(payload))
	}
	buf := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint32(buf[:LenSize], uint32(len(payload)))
	copy(buf[LenSize:], payload)
	sum := crc32.ChecksumIEEE(payload)
	binary.BigEndian.PutUint32(buf[LenSize+len(payload):], sum)
	return buf, nil
}

// EncodedLen 返回编码 payload 所需的字节数。
func EncodedLen(payloadLen int) int {
	return HeaderSize + payloadLen
}

// Decode 从 buf 开头尝试解出一条记录，只读 buf 本身，绝不越界。
// buf 为空时返回 Truncated（零字节无法构成任何记录）。
func Decode(buf []byte) Result {
	if len(buf) < LenSize {
		return Result{Kind: Truncated, Missing: -1}
	}
	n := binary.BigEndian.Uint32(buf[:LenSize])
	if n > MaxPayload {
		// 长度前缀声称的长度不可能是合法编码，视为损坏。
		return Result{Kind: Corrupt}
	}
	total := HeaderSize + int(n)
	if len(buf) < total {
		return Result{Kind: Truncated, Missing: total - len(buf)}
	}
	payload := buf[LenSize : LenSize+int(n)]
	want := binary.BigEndian.Uint32(buf[LenSize+int(n) : total])
	if crc32.ChecksumIEEE(payload) != want {
		return Result{Kind: Corrupt}
	}
	out := make([]byte, int(n))
	copy(out, payload)
	return Result{Kind: Complete, Payload: out, Size: total}
}
