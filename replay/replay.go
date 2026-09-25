// Package replay 从快照出发重放事件，依赖 es。
package replay

import (
	"errors"
	"sort"

	"ontology/es"
)

// ErrBadReplay 重放非法：列表内 Seq 乱序（递减），或含 Seq<=0 的事件。
var ErrBadReplay = errors.New("replay: invalid event list")

// Replayer 重放器。locateCmp 为非导出计数器，记录最近一次重放
// 「定位第一条 Seq>快照.Seq 的事件」所做的比较次数，不出现在公开接口。
type Replayer struct {
	locateCmp int
}

// New 返回一个重放器。
func New() *Replayer { return &Replayer{} }

// Replay 从 snap 出发，只按 Seq 升序应用 Seq>snap.Seq 的事件；
// Seq<=snap.Seq 的事件跳过（幂等），重复 Seq 不重复生效。
// 列表含 Seq<=0 或 Seq 递减时整体失败返回 ErrBadReplay，不产生任何结果。
func (r *Replayer) Replay(snap es.Snapshot, evs []es.Event) (int64, error) {
	if err := es.ValidateSnapshot(snap); err != nil {
		return 0, err
	}
	// 前置校验：先整体验证，再应用，失败不留痕。
	for i, ev := range evs {
		if ev.Seq <= 0 {
			return 0, ErrBadReplay
		}
		if i > 0 && ev.Seq < evs[i-1].Seq {
			return 0, ErrBadReplay
		}
	}
	start := r.locate(snap.Seq, evs)
	bal, last := snap.Total, snap.Seq
	for _, ev := range evs[start:] {
		if ev.Seq <= last { // 已含在快照或已应用过：跳过，幂等
			continue
		}
		bal = es.Apply(bal, ev)
		last = ev.Seq
	}
	return bal, nil
}

// locate 二分查找首个 Seq>seq 的下标，比较次数计入 locateCmp，O(log n)。
func (r *Replayer) locate(seq int64, evs []es.Event) int {
	r.locateCmp = 0
	return sort.Search(len(evs), func(i int) bool {
		r.locateCmp++
		return evs[i].Seq > seq
	})
}
