// Package fifo 维护单个 key 的序号状态：next 指针、有序缓冲、级联放行、重复判定与背压。
// 不依赖其他包。非并发安全，由上层 dispatch 加锁。
package fifo

import (
	"errors"
	"sort"
)

// ErrBackpressure 缓冲将超过 maxInFlight 时返回；拒绝先于任何状态修改，不留痕。
var ErrBackpressure = errors.New("fifo: backpressure limit exceeded")

// Buffer 是单个 key 的 FIFO 乱序防护缓冲区。
type Buffer struct {
	next    int64   // 下一个待发射的序号，初始 1
	buf     []int64 // 按 Seq 升序的缓冲，活动区间为 buf[head:]
	head    int     // 队首指针：级联放行每步只检查队首，O(1) 定位
	max     int     // 最大在途缓冲数
	dropped int64   // 重复丢弃计数（seq < next 的到达次数）
	probes  int64   // 最近一次级联中为判断「缓冲是否含 next」检查过的条目数（非导出）
}

// New 创建容量为 maxInFlight 的单 key 缓冲区。
func New(maxInFlight int) *Buffer {
	return &Buffer{next: 1, max: maxInFlight}
}

// Feed 投递一个序号：seq<next 判重复丢弃；seq==next 发射并级联放行连续前缀；
// seq>next 按升序插入缓冲，若会使缓冲超过 max 则整体拒绝（ErrBackpressure）。
// 返回本次新发射的序号（含级联）。缓冲中已存在的在途序号视为重复投递，幂等忽略且不计丢弃。
func (b *Buffer) Feed(seq int64) ([]int64, error) {
	// 分支 1：已发射过的序号 → 重复，幂等丢弃。
	if seq < b.next {
		b.dropped++
		return nil, nil
	}
	// 分支 2：正好轮到 → 发射，随后级联放行缓冲中的连续前缀。
	if seq == b.next {
		emitted := []int64{seq}
		b.next++
		b.probes = 0
		for b.head < len(b.buf) {
			b.probes++ // 本次只检查队首这一条
			if b.buf[b.head] != b.next {
				break // 出现空洞：后续条目必更大，立即停止
			}
			emitted = append(emitted, b.buf[b.head])
			b.head++
			b.next++
		}
		if b.head > 0 { // 回收已放行的前缀
			b.buf = append(b.buf[:0], b.buf[b.head:]...)
			b.head = 0
		}
		return emitted, nil
	}
	// 分支 3：seq > next → 入缓冲。先判背压，再做任何修改（失败不留痕）。
	if len(b.buf)-b.head+1 > b.max {
		return nil, ErrBackpressure
	}
	pos := sort.Search(len(b.buf)-b.head, func(i int) bool {
		return b.buf[b.head+i] >= seq
	})
	if pos < len(b.buf)-b.head && b.buf[b.head+pos] == seq {
		return nil, nil // 在途序号的重复投递：幂等，不发射、不计数、不占新槽
	}
	b.buf = append(b.buf, 0)
	copy(b.buf[b.head+pos+1:], b.buf[b.head+pos:])
	b.buf[b.head+pos] = seq
	return nil, nil
}

// Next 返回下一个待发射的序号；已发射序列恒为 [1, Next())。
func (b *Buffer) Next() int64 { return b.next }

// Len 返回当前在途缓冲的条目数。
func (b *Buffer) Len() int { return len(b.buf) - b.head }

// Buffered 返回当前在途缓冲的升序副本。
func (b *Buffer) Buffered() []int64 {
	out := make([]int64, len(b.buf)-b.head)
	copy(out, b.buf[b.head:])
	return out
}

// Dropped 返回本 key 的重复丢弃总数。
func (b *Buffer) Dropped() int64 { return b.dropped }
