// Package dnsname 实现 DNS 报文名字压缩（RFC 1035 第 4.1.4 节）的编码器与解码器。
//
// 编码器在同一报文内登记已写出的名字后缀，重复后缀只写一次，
// 后续以两字节指针（高两位 11，低 14 位为距报文起始的偏移）引用。
// 后缀比较按 ASCII 不分大小写，命中后解码得到的是首次写入时的大小写。
package dnsname

import (
	"errors"
	"sync"
)

const (
	// DefaultBase 是报文构造器的默认起始偏移（DNS 头部 12 字节）。
	DefaultBase = 12
	// MaxPointerOffset 是可被指针引用（因而被登记）的最大起点偏移。
	MaxPointerOffset = 16383
	// MaxNameLen 是名字线格式（含结尾）的最大长度。
	MaxNameLen = 255
	// MaxMessageLen 是报文最大总长。
	MaxMessageLen = 65535
	// MaxLabelLen 是单个标签的最大字节数。
	MaxLabelLen = 63
)

// 解码错误，可用 errors.Is 区分。
var (
	// ErrReservedBits 标签长度字节高两位为 01 或 10。
	ErrReservedBits = errors.New("dnsname: 标签长度字节高两位为保留值 01/10")
	// ErrTruncated 标签或指针被报文末尾截断。
	ErrTruncated = errors.New("dnsname: 标签或指针被报文末尾截断")
	// ErrPointerNotBackward 指针目标不小于该指针自身的起始偏移（含指向自身）。
	ErrPointerNotBackward = errors.New("dnsname: 指针目标未严格小于指针自身偏移")
	// ErrPointerBeforeBase 指针目标小于报文名字区起始 base。
	ErrPointerBeforeBase = errors.New("dnsname: 指针目标小于 base")
	// ErrNameTooLong 展开后名字线格式总长超过 255。
	ErrNameTooLong = errors.New("dnsname: 展开后名字总长超过 255")
)

// 编码错误，可用 errors.Is 区分。
var (
	// ErrEmptyLabel 名字含空标签。
	ErrEmptyLabel = errors.New("dnsname: 标签为空")
	// ErrLabelTooLong 标签超过 63 字节。
	ErrLabelTooLong = errors.New("dnsname: 标签超过 63 字节")
	// ErrMessageTooLong 写入会使报文总长超过 65535。
	ErrMessageTooLong = errors.New("dnsname: 写入将使报文总长超过 65535")
)

// Encoder 是 DNS 报文名字压缩构造器，可被并发调用，
// 并发结果等价于某个串行顺序，登记表与已写字节始终一致。
type Encoder struct {
	mu    sync.Mutex
	msg   []byte
	table map[string]int // 后缀（线格式、ASCII 小写）-> 起点偏移
}

// NewEncoder 创建构造器，逻辑偏移从 base（默认 DefaultBase）开始。
// 构造器在底层切片中保留 base 个占位字节，使名字起点偏移即报文偏移。
func NewEncoder(base ...int) *Encoder {
	b := DefaultBase
	if len(base) > 0 {
		b = base[0]
	}
	return &Encoder{
		msg:   make([]byte, b, b+512),
		table: make(map[string]int),
	}
}

// Bytes 返回已构造的报文字节副本（含 base 之前的占位区）。
func (e *Encoder) Bytes() []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]byte, len(e.msg))
	copy(out, e.msg)
	return out
}

// Len 返回当前报文总长。
func (e *Encoder) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.msg)
}

// Write 将名字（标签序列，空切片表示根名字）追加到报文末尾，
// 返回该名字的起点偏移。写入失败时不追加任何字节也不登记任何后缀。
func (e *Encoder) Write(labels []string) (int, error) {
	wireLen := 1
	for _, l := range labels {
		if len(l) == 0 {
			return 0, ErrEmptyLabel
		}
		if len(l) > MaxLabelLen {
			return 0, ErrLabelTooLong
		}
		wireLen += 1 + len(l)
	}
	if wireLen > MaxNameLen {
		return 0, ErrNameTooLong
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 从整名起逐次去掉最先标签，找第一个（最长）已登记后缀。
	hit := -1
	hitOff := 0
	for i := 0; i < len(labels); i++ {
		if off, ok := e.table[suffixKey(labels[i:])]; ok {
			hit, hitOff = i, off
			break
		}
	}

	prefix := len(labels)
	if hit >= 0 {
		prefix = hit
	}
	written := 0
	for i := 0; i < prefix; i++ {
		written += 1 + len(labels[i])
	}
	if hit >= 0 {
		written += 2
	} else {
		written++
	}
	if len(e.msg)+written > MaxMessageLen {
		return 0, ErrMessageTooLong
	}

	start := len(e.msg)
	pos := start
	for i := 0; i < prefix; i++ {
		// 未被指针覆盖的各后缀在写出时登记起点偏移（>16383 不登记）。
		if pos <= MaxPointerOffset {
			e.table[suffixKey(labels[i:])] = pos
		}
		e.msg = append(e.msg, byte(len(labels[i])))
		e.msg = append(e.msg, labels[i]...)
		pos += 1 + len(labels[i])
	}
	if hit >= 0 {
		e.msg = append(e.msg, 0xC0|byte(hitOff>>8), byte(hitOff))
	} else {
		e.msg = append(e.msg, 0)
	}
	return start, nil
}

// suffixKey 返回后缀的登记表键：线格式（长度字节+内容）内容按 ASCII 小写化。
func suffixKey(labels []string) string {
	n := 0
	for _, l := range labels {
		n += 1 + len(l)
	}
	b := make([]byte, 0, n)
	for _, l := range labels {
		b = append(b, byte(len(l)))
		for i := 0; i < len(l); i++ {
			b = append(b, asciiLower(l[i]))
		}
	}
	return string(b)
}

func asciiLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// Decode 从 msg 的 off 处解码一个名字，base 为报文名字区起始偏移。
// 返回标签序列与消费字节数（遇到指针时消费到指针的两个字节为止）。
// 解码可并发作用于同一报文。错误按读取顺序只报第一个，可用 errors.Is 区分。
func Decode(msg []byte, off, base int) ([]string, int, error) {
	var labels []string
	pos := off
	consumed := -1
	expanded := 0
	for {
		if pos < 0 || pos >= len(msg) {
			return nil, 0, ErrTruncated
		}
		b := msg[pos]
		switch {
		case b&0xC0 == 0xC0:
			if pos+1 >= len(msg) {
				return nil, 0, ErrTruncated
			}
			target := int(b&0x3F)<<8 | int(msg[pos+1])
			if target < base {
				return nil, 0, ErrPointerBeforeBase
			}
			if target >= pos {
				return nil, 0, ErrPointerNotBackward
			}
			if consumed < 0 {
				consumed = pos + 2 - off
			}
			pos = target
		case b&0xC0 != 0:
			return nil, 0, ErrReservedBits
		case b == 0:
			expanded++
			if expanded > MaxNameLen {
				return nil, 0, ErrNameTooLong
			}
			if consumed < 0 {
				consumed = pos + 1 - off
			}
			return labels, consumed, nil
		default:
			l := int(b)
			if pos+1+l > len(msg) {
				return nil, 0, ErrTruncated
			}
			labels = append(labels, string(msg[pos+1:pos+1+l]))
			expanded += 1 + l
			if expanded > MaxNameLen {
				return nil, 0, ErrNameTooLong
			}
			pos += 1 + l
		}
	}
}
