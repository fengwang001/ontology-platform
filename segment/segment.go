package segment

import (
	"fmt"

	"ontology/codec"
)

// Segment 在一段 Buffer 上追加记录并顺序扫描。
// 它不是并发安全的，并发保护由上层的 wal 负责。
type Segment struct {
	buf Buffer
}

// New 在 buf 上创建一个段。
func New(buf Buffer) *Segment {
	return &Segment{buf: buf}
}

// Len 返回段当前的字节数。
func (s *Segment) Len() int {
	return s.buf.Len()
}

// Append 把 payload 编码成一条记录追加到段尾，
// 返回该记录的起始字节偏移。
func (s *Segment) Append(payload []byte) (int, error) {
	rec, err := codec.Encode(payload)
	if err != nil {
		return 0, fmt.Errorf("segment: append: %w", err)
	}
	off := s.buf.Len()
	s.buf.Append(rec)
	return off, nil
}

// Stop 描述一次扫描为何停止、停在哪里。
type Stop struct {
	Offset int        // 停止点的字节偏移（第一条无法回放的记录起点）
	Reason codec.Kind // 停止原因：Truncated 或 Corrupt；扫满 limit 时为 Complete
}

// Scan 从偏移 0 开始顺序解码，最多读到 limit 字节（limit 超过
// 缓冲长度时按缓冲长度算）。每解出一条完整记录就调用 fn；
// fn 返回 false 可提前结束扫描。
//
// 遇到半条记录或校验不符立即停止，停止点之后的字节一律不解释。
// 返回成功回放的记录数与停止信息。
func (s *Segment) Scan(limit int, fn func(offset int, payload []byte) bool) (int, Stop) {
	data := s.buf.Bytes()
	if limit > len(data) {
		limit = len(data)
	}
	if limit < 0 {
		limit = 0
	}
	count := 0
	off := 0
	for off < limit {
		res := codec.Decode(data[off:limit])
		if res.Kind != codec.Complete {
			return count, Stop{Offset: off, Reason: res.Kind}
		}
		if !fn(off, res.Payload) {
			return count, Stop{Offset: off, Reason: codec.Complete}
		}
		count++
		off += res.Size
	}
	return count, Stop{Offset: off, Reason: codec.Complete}
}
