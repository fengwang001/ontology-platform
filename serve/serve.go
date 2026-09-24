// Package serve 是对外组装器：串起 Range 头解析、区间归一化、
// 从字节源循环补齐取数、multipart 封装与断点续写。
//
// 并发约定：单个 Assembler 不支持并发写出，Build/Write 须由同一
// goroutine 串行调用；多 goroutine 各持独立 Assembler 组装不同
// 响应是安全的。
package serve

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/source"
)

// 三类超限与短读错误，彼此可用 errors.Is 判定。
var (
	ErrTooManyRanges = errors.New("serve: range count exceeds limit")
	ErrTooManyBytes  = errors.New("serve: response bytes exceed limit")
	ErrBoundaryTries = errors.New("serve: boundary generation retries exhausted")
	ErrShortRead     = errors.New("serve: source hit EOF before range was filled")
)

// Limits 是资源上限；零值表示不限制。
type Limits struct {
	MaxRanges int   // 区间个数上限（解析后、归一化前）
	MaxBytes  int64 // 单次响应总字节上限
	MaxTries  int   // 边界串生成重试次数上限
	// Rand 是边界串随机源；nil 时使用 crypto/rand。
	// 测试可注入确定性源以复现边界串冲突。
	Rand io.Reader
}

// Stats 是只读查询快照；未开始组装时为零值。
type Stats struct {
	Ranges    []coalesce.Range // 归一化后的区间列表（拷贝）
	Total     int              // 响应总字节数
	Written   int              // 已写出字节数
	Multipart bool             // 是否封装为 multipart
}

// Assembler 组装一个 Range 响应。状态：不可变 body + 写出游标 off，
// 不变量：已写出字节 == body[:off]，故任意切分点续写结果完全相同。
type Assembler struct {
	src   source.Source
	lim   Limits
	body  []byte
	off   int
	rngs  []coalesce.Range
	multi bool
}

// New 创建一个组装器。
func New(src source.Source, lim Limits) *Assembler { return &Assembler{src: src, lim: lim} }

// Build 解析 Range 头并完成组装。任何失败（含三类超限）都不改变
// 已有状态：所有中间结果先落在局部变量，全部通过后才一次性提交。
func (a *Assembler) Build(header string) error {
	specs, err := rangespec.Parse(header)
	if err != nil {
		return err
	}
	if a.lim.MaxRanges > 0 && len(specs) > a.lim.MaxRanges {
		return ErrTooManyRanges
	}
	total := a.src.Size()
	rngs, err := coalesce.Normalize(specs, total)
	if err != nil {
		return err
	}
	data, err := a.readAll(rngs)
	if err != nil {
		return err
	}
	body, multi, err := wrap(rngs, data, total, a.lim)
	if err != nil {
		return err
	}
	if a.lim.MaxBytes > 0 && int64(len(body)) > a.lim.MaxBytes {
		return ErrTooManyBytes
	}
	a.body, a.rngs, a.multi, a.off = body, rngs, multi, 0
	return nil
}

// readAll 循环补齐每个区间的字节；EOF 但字节不足返回 ErrShortRead。
func (a *Assembler) readAll(rngs []coalesce.Range) ([]byte, error) {
	var buf []byte
	for _, r := range rngs {
		want := int(r.End - r.Start + 1)
		chunk := make([]byte, want)
		n := 0
		for n < want {
			m, err := a.src.ReadAt(chunk[n:], r.Start+int64(n))
			n += m
			if n >= want {
				break
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil, ErrShortRead
				}
				return nil, err
			}
			if m == 0 {
				return nil, ErrShortRead // 无进展，防死循环
			}
		}
		buf = append(buf, chunk...)
	}
	return buf, nil
}

// wrap 单区间返回裸字节，多区间封装为 multipart/byteranges。
func wrap(rngs []coalesce.Range, data []byte, total int64, lim Limits) ([]byte, bool, error) {
	if len(rngs) == 1 {
		return data, false, nil
	}
	maxTries := lim.MaxTries
	if maxTries <= 0 {
		maxTries = 8
	}
	rng := lim.Rand
	if rng == nil {
		rng = rand.Reader
	}
	boundary, err := multipart.Boundary(data, maxTries, rng)
	if err != nil {
		return nil, false, ErrBoundaryTries
	}
	var buf bytes.Buffer
	off := 0
	for _, r := range rngs {
		n := int(r.End - r.Start + 1)
		buf.Write(multipart.Header(boundary, "application/octet-stream", r, total))
		buf.Write(data[off : off+n])
		buf.Write(multipart.Separator())
		off += n
	}
	buf.Write(multipart.Closing(boundary))
	return buf.Bytes(), true, nil
}

// Write 从断点续写：拷贝 body[off:] 的前 len(p) 字节并推进游标，
// 支持短写；写完后返回 io.EOF。
func (a *Assembler) Write(p []byte) (int, error) {
	if a.off >= len(a.body) {
		return 0, io.EOF
	}
	n := copy(p, a.body[a.off:])
	a.off += n
	return n, nil
}

// Rewind 把写出游标重置到开头，便于用不同切分重复写出同一响应。
func (a *Assembler) Rewind() { a.off = 0 }

// Stats 返回只读快照；不推进任何状态，连查两次结果相同。
func (a *Assembler) Stats() Stats {
	s := Stats{Total: len(a.body), Written: a.off, Multipart: a.multi}
	if a.rngs != nil {
		s.Ranges = append([]coalesce.Range(nil), a.rngs...)
	}
	return s
}
