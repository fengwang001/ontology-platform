// Package codec 定义预写日志单条记录的编码与解码。
//
// 记录布局（大端序）：
//
//	[4 字节负载长度][负载][4 字节 CRC32 校验和]
//
// 解码只依赖字节流本身，可判定三种互斥结果：
// 完整记录、半条记录（截断）、校验和不符。
package codec

import (
	"encoding/binary"
	"hash/crc32"
)

const (
	// PrefixLen 是长度前缀的字节数。
	PrefixLen = 4
	// ChecksumLen 是校验和的字节数。
	ChecksumLen = 4
	// HeaderLen 是不含负载的固定开销（长度前缀 + 校验和）。
	HeaderLen = PrefixLen + ChecksumLen
)

// Kind 是解码结果的类别，三种取值彼此可判定。
type Kind int

const (
	// KindOK 表示解出一条完整且校验通过的记录。
	KindOK Kind = iota
	// KindTruncated 表示字节流在这里只剩半条记录。
	KindTruncated
	// KindCorrupt 表示记录完整但校验和不符。
	KindCorrupt
)

// TruncReason 进一步区分半条记录停在哪一部分。
type TruncReason int

const (
	// TruncNone 表示没有截断（Kind 不是 KindTruncated）。
	TruncNone TruncReason = iota
	// TruncPrefix 表示连 4 字节长度前缀都不完整。
	TruncPrefix
	// TruncPayload 表示长度前缀完整，但负载不完整。
	TruncPayload
	// TruncChecksum 表示负载完整（或为零长度），但校验和不完整。
	TruncChecksum
)

// Result 是一次解码的完整结论。
type Result struct {
	Kind     Kind        // 三态类别
	Reason   TruncReason // 仅当 Kind == KindTruncated 有效
	Payload  []byte      // 仅当 Kind == KindOK 有效，是新分配的副本
	Consumed int         // 仅当 Kind == KindOK 有效，本条记录占用的字节数
	Declared uint32      // 长度前缀可读时，其声称的负载长度
	Need     int         // 仅当 Kind == KindTruncated 有效，还差多少字节才完整
}

// EncodedLen 返回负载长度为 payloadLen 的记录编码后的总字节数。
func EncodedLen(payloadLen int) int {
	return HeaderLen + payloadLen
}

// Encode 把负载编码成一条完整记录，返回新分配的字节切片。
func Encode(payload []byte) []byte {
	buf := make([]byte, EncodedLen(len(payload)))
	binary.BigEndian.PutUint32(buf[:PrefixLen], uint32(len(payload)))
	copy(buf[PrefixLen:], payload)
	sum := crc32.ChecksumIEEE(payload)
	binary.BigEndian.PutUint32(buf[PrefixLen+len(payload):], sum)
	return buf
}

// Decode 从 buf 起始处解一条记录，只靠字节流本身判定结果。
// buf 为空时返回 KindTruncated / TruncPrefix。
func Decode(buf []byte) Result {
	if len(buf) < PrefixLen {
		return Result{
			Kind:   KindTruncated,
			Reason: TruncPrefix,
			Need:   PrefixLen - len(buf),
		}
	}
	declared := binary.BigEndian.Uint32(buf[:PrefixLen])
	// 用 uint64 计算避免 int 溢出。
	total := uint64(HeaderLen) + uint64(declared)
	if total > uint64(len(buf)) {
		reason := TruncPayload
		if uint64(PrefixLen)+uint64(declared) < uint64(len(buf)) {
			reason = TruncChecksum
		}
		return Result{
			Kind:     KindTruncated,
			Reason:   reason,
			Declared: declared,
			Need:     int(total - uint64(len(buf))),
		}
	}
	payload := buf[PrefixLen : PrefixLen+int(declared)]
	sumOff := PrefixLen + int(declared)
	want := binary.BigEndian.Uint32(buf[sumOff : sumOff+ChecksumLen])
	if crc32.ChecksumIEEE(payload) != want {
		return Result{Kind: KindCorrupt, Declared: declared}
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return Result{
		Kind:     KindOK,
		Payload:  out,
		Consumed: int(total),
		Declared: declared,
	}
}
