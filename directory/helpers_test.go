package directory

import (
	"fmt"
	"sort"
	"strings"
)

// dump 生成完整状态快照，用于验证被拒绝操作不产生任何副作用。
func (s *Simulator) dump() string {
	var b strings.Builder
	for i, ch := range s.caches {
		fmt.Fprintf(&b, "cache%d=%v; ", i, ch.snapshot())
	}
	blocks := make([]int, 0, len(s.dir))
	for blk := range s.dir {
		blocks = append(blocks, blk)
	}
	sort.Ints(blocks)
	for _, blk := range blocks {
		d := s.dir[blk]
		hs := make([]int, 0, len(d.holders))
		for h := range d.holders {
			hs = append(hs, h)
		}
		sort.Ints(hs)
		fmt.Fprintf(&b, "dir%d(holders=%v,mod=%d,mem=%d); ", blk, hs, d.modifier, s.mem[blk])
	}
	return b.String()
}
