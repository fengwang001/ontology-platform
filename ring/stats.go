package ring

import (
	"fmt"

	"ontology/cursor"
)

// Stats 返回容量、序号区间、读者数、最慢落后量与掉队计数的一致快照。
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()

	old, _ := b.oldest()
	return Stats{
		Capacity:    b.cap,
		Live:        b.live,
		Oldest:      old,
		Head:        b.next - 1,
		Readers:     len(b.readers),
		SlowestLag:  b.stat.Slowest(),
		Behind:      b.stat.BehindCount(),
		Overwritten: b.overwritten,
	}
}

// LagDistribution 返回每个存活读者相对最新序号的落后量（含掉队者）。
func (b *Buffer) LagDistribution() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	head := b.next - 1
	out := make([]int64, 0, len(b.readers))
	for c := range b.readers {
		out = append(out, c.Lag(head))
	}
	return out
}

// SelfCheck 一次性核验：存活读者位置合法（区间内或已标记掉队），
// 且增量统计（最慢落后量、掉队计数）与逐个读者核对完全一致。
func (b *Buffer) SelfCheck() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	head := b.next - 1
	old, hasOld := b.oldest()
	active, behind := 0, 0
	var slowest int64
	for c := range b.readers {
		switch c.Status() {
		case cursor.Closed:
			return fmt.Errorf("selfcheck: closed cursor still registered")
		case cursor.Behind:
			behind++
			if !hasOld || c.Want() >= old {
				return fmt.Errorf("selfcheck: cursor marked behind but want=%d in range", c.Want())
			}
		case cursor.Active:
			active++
			if hasOld && c.Want() < old {
				return fmt.Errorf("selfcheck: active cursor want=%d older than oldest=%d", c.Want(), old)
			}
			if lag := c.Lag(head); lag > slowest {
				slowest = lag
			}
		}
	}
	if got := b.stat.Slowest(); got != slowest {
		return fmt.Errorf("selfcheck: slowest lag stat=%d manual=%d", got, slowest)
	}
	if got := b.stat.BehindCount(); got != behind {
		return fmt.Errorf("selfcheck: behind count stat=%d manual=%d", got, behind)
	}
	if active+behind != len(b.readers) {
		return fmt.Errorf("selfcheck: reader count mismatch")
	}
	return nil
}
