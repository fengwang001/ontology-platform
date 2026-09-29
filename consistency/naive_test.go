package consistency

import (
	"errors"
	"fmt"
	"math"
)

// naiveView 是保留全部历史的朴素参照视图。
type naiveView struct {
	progress Timestamp
	versions []version
}

type naiveStore struct {
	retain int
	views  map[string]*naiveView
	order  []string
	last   Timestamp
}

func newNaive(retain int, views ...string) *naiveStore {
	n := &naiveStore{retain: retain, views: map[string]*naiveView{}}
	for _, name := range views {
		n.order = append(n.order, name)
		n.views[name] = &naiveView{progress: -1}
	}
	return n
}

func (n *naiveStore) apply(view string, ts Timestamp, value Value) error {
	if view == "" || value == nil || ts < 0 {
		return ErrInvalidArgument
	}
	vs, ok := n.views[view]
	if !ok {
		return ErrInvalidArgument
	}
	if ts <= vs.progress {
		return ErrTimestampNotAdvancing
	}
	vs.versions = append(vs.versions, version{ts: ts, value: value})
	vs.progress = ts
	return nil
}

func (n *naiveStore) heartbeat(view string, ts Timestamp) error {
	if view == "" || ts < 0 {
		return ErrInvalidArgument
	}
	vs, ok := n.views[view]
	if !ok {
		return ErrInvalidArgument
	}
	if ts <= vs.progress {
		return ErrTimestampNotAdvancing
	}
	vs.progress = ts
	return nil
}

func (n *naiveStore) minProgress() (Timestamp, bool) {
	min := Timestamp(math.MaxInt64)
	for _, name := range n.order {
		vs := n.views[name]
		if len(vs.versions) == 0 {
			return 0, false
		}
		if vs.progress < min {
			min = vs.progress
		}
	}
	return min, true
}

// maxOldest 模拟有限保留：全历史中窗口下界为倒数第 retain 个版本。
func (n *naiveStore) maxOldest() Timestamp {
	oldest := Timestamp(math.MinInt64)
	for _, name := range n.order {
		vs := n.views[name]
		idx := len(vs.versions) - n.retain
		if idx < 0 {
			idx = 0
		}
		if ts := vs.versions[idx].ts; ts > oldest {
			oldest = ts
		}
	}
	return oldest
}

// readAt 与 Store.ReadAt 同样的两关判定，但从全历史中取值。
func (n *naiveStore) readAt(at Timestamp) (*Snapshot, error) {
	if at < 0 {
		return nil, ErrInvalidArgument
	}
	minProgress, ok := n.minProgress()
	if !ok || at > minProgress {
		return nil, ErrNotReady
	}
	if at < n.maxOldest() {
		return nil, ErrTooOld
	}
	views := make([]ViewSnapshot, 0, len(n.order))
	for _, name := range n.order {
		versions := n.views[name].versions
		chosen := -1
		for i := len(versions) - 1; i >= 0; i-- {
			if versions[i].ts <= at {
				chosen = i
				break
			}
		}
		views = append(views, ViewSnapshot{
			View:      name,
			At:        at,
			VersionTs: versions[chosen].ts,
			Value:     versions[chosen].value,
		})
	}
	return &Snapshot{At: at, Views: views}, nil
}

// read 与 Store.Read 相同的选点算法。
func (n *naiveStore) read() (*Snapshot, error) {
	minProgress, ok := n.minProgress()
	if !ok {
		return nil, ErrNotReady
	}
	oldest := n.maxOldest()
	at := minProgress
	if n.last > at {
		at = n.last
	}
	if at < oldest {
		return nil, ErrTooOld
	}
	snap, err := n.readAt(at)
	if err != nil {
		return nil, err
	}
	n.last = at
	return snap, nil
}

// valueAt 直接从全历史取视图在 at 的值（调用方自行保证存在 <=at 的版本）。
func (n *naiveStore) valueAt(view string, at Timestamp) Value {
	versions := n.views[view].versions
	for i := len(versions) - 1; i >= 0; i-- {
		if versions[i].ts <= at {
			return versions[i].value
		}
	}
	return nil
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrTimestampNotAdvancing):
		return "not-advancing"
	case errors.Is(err, ErrNotReady):
		return "not-ready"
	case errors.Is(err, ErrTooOld):
		return "too-old"
	default:
		return fmt.Sprintf("unknown:%v", err)
	}
}

func snapshotsEqual(a, b *Snapshot) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.At != b.At || len(a.Views) != len(b.Views) {
		return false
	}
	for i := range a.Views {
		x, y := a.Views[i], b.Views[i]
		if x.View != y.View || x.At != y.At || x.VersionTs != y.VersionTs || x.Value != y.Value {
			return false
		}
	}
	return true
}
