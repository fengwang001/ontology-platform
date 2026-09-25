// Package export 在快照副本上做分块导出、done 判定与位点前进。
package export

import (
	"errors"
	"fmt"

	"ontology/snap"
)

// ErrFinished 表示快照已导出完毕，再次导出被拒绝。
var ErrFinished = errors.New("export: snapshot already finished")

// Exporter 把一个快照分块导出，记录完成态与定位代价。
// 不是并发安全的，调用方（api 包）负责串行化。
type Exporter struct {
	snap       *snap.Snapshot
	size       int
	done       bool
	probeCount int // 最近一次 Next 为定位位点后第一个键而检查的键个数（含本块逐键取出）
}

// New 在快照 s 上建一个每块至多 chunkSize 条的导出器。
func New(s *snap.Snapshot, chunkSize int) *Exporter {
	return &Exporter{snap: s, size: chunkSize}
}

// Next 返回 key 严格大于 cursor 的下一块（至多 chunkSize 条）。
// newCursor 为本块最后一键；本块导完后没有更多键时 done=true
// （末块即使是满块也 done=true）；done 之后再调返回 ErrFinished。
func (e *Exporter) Next(cursor string) ([]snap.Entry, string, bool, error) {
	if e.done {
		return nil, "", false, ErrFinished
	}
	lo, probes := e.snap.After(cursor)
	end := lo + e.size
	if end > e.snap.Len() {
		end = e.snap.Len()
	}
	entries := make([]snap.Entry, 0, end-lo)
	for i := lo; i < end; i++ {
		entries = append(entries, e.snap.At(i))
	}
	e.probeCount = probes + len(entries)
	newCursor := cursor
	if len(entries) > 0 {
		newCursor = entries[len(entries)-1].Key
	}
	e.done = end == e.snap.Len() // 判据：位点之后没有更多键，与块是否满无关
	return entries, newCursor, e.done, nil
}

// Resume 等价于 Next：把上次的 newCursor 传回即可续传，
// 位点键本身绝不重复导出。
func (e *Exporter) Resume(cursor string) ([]snap.Entry, string, bool, error) {
	return e.Next(cursor)
}

// maxLocateProbes 是定位代价的上界：二分定位 log2(10000)<14，取 20 留余量；
// 若退化成从头线性扫描，n=10000 时要查约 5000 个键，必然超限。
const maxLocateProbes = 20

// CheckProbes 自检：各规模 n 下从中间位点定位，检查的键个数
// 不超过小常数+本块条数（不随 n 线性增长）。只暴露布尔结论。
func CheckProbes() bool {
	const chunk = 16
	for _, n := range []int{100, 1000, 10000} {
		m := make(map[string]int64, n)
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%06d", i)] = int64(i)
		}
		e := New(snap.Capture(m), chunk)
		if _, _, _, err := e.Next(fmt.Sprintf("k%06d", n/2)); err != nil {
			return false
		}
		if e.probeCount > maxLocateProbes+chunk {
			return false
		}
	}
	return true
}
