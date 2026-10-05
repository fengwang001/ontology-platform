// Package snap 维护周期快照：帧号为 P 的倍数的帧被追加时生成快照，
// 快照大小为到该帧为止（含）最近 P 帧的 size 之和；只保留最新一份。
package snap

// Snapshot 描述一份快照。
type Snapshot struct {
	Tick int64 // 快照帧号 s
	Size int64 // 到 s 为止（含）最近 P 帧的 size 之和
}

// Tracker 跟踪最新快照。
type Tracker struct {
	p      int64
	latest Snapshot
	ok     bool
}

// NewTracker 创建周期为 p 的快照跟踪器。
func NewTracker(p int64) *Tracker {
	return &Tracker{p: p}
}

// Consider 在帧 cur 被追加后调用；仅当 cur 为 P 的倍数时生成快照。
// windowSum 惰性求值"到 cur 为止最近 P 帧的 size 之和"，非快照帧不调用。
func (t *Tracker) Consider(cur int64, windowSum func() int64) (Snapshot, bool) {
	if cur <= 0 || cur%t.p != 0 {
		return Snapshot{}, false
	}
	t.latest = Snapshot{Tick: cur, Size: windowSum()}
	t.ok = true
	return t.latest, true
}

// Latest 返回最新快照；尚无快照时 ok 为 false。
func (t *Tracker) Latest() (snap Snapshot, ok bool) {
	return t.latest, t.ok
}
