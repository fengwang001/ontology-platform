package presence

import "container/heap"

// device is one registered device. Its heap index is kept for O(log d)
// lease updates where d <= 8, so all device operations are effectively O(1).
type device struct {
	id     string
	status Status
	expiry int64
	index  int
	// originSeq is the global arrival sequence of the Report that scheduled
	// the current expiry. It serves as the same-instant tiebreaker, so an
	// expiry event sorts where its originating operation linearized rather
	// than where lazy processing happened to visit the target.
	originSeq uint64
}

// expiryHeap orders active devices by expiry, breaking ties by id so that
// same-expiry devices have a deterministic arrival-independent order at the
// data level; final same-time ordering is decided by operation arrival.
type expiryHeap []*device

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	if h[i].originSeq != h[j].originSeq {
		return h[i].originSeq < h[j].originSeq
	}
	return h[i].id < h[j].id
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *expiryHeap) Push(x any) {
	d := x.(*device)
	d.index = len(*h)
	*h = append(*h, d)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.index = -1
	*h = old[:n-1]
	return d
}

// sub is a viewer's subscription to this user (the sub lives on the target).
// pending holds notifications for the (viewer, target) pair, ordered by
// (effective time, originating operation arrival seq). last is the previously notified
// visible status, guaranteeing consecutive states chain correctly.
type sub struct {
	last    Status
	pending []Notification
}

// userRec is all per-user state. Every field is guarded by its shard mutex.
type userRec struct {
	id        string
	devices   map[string]*device
	expiries  expiryHeap
	activeCnt int
	real      Status
	lastNow   int64
	invisible bool

	// blocked[viewer] == true means this user blocks the viewer.
	blocked map[string]bool

	// subs[viewer] exists iff viewer is subscribed to this user.
	subs map[string]*sub

	// subscribedTo is the inverse edge set, used by Drain to find targets.
	subscribedTo map[string]bool
}

func newUserRec(id string) *userRec {
	u := &userRec{
		id:           id,
		devices:      make(map[string]*device),
		blocked:      make(map[string]bool),
		subs:         make(map[string]*sub),
		subscribedTo: make(map[string]bool),
	}
	heap.Init(&u.expiries)
	return u
}

// recompute derives the real aggregated status from active devices.
// Aggregation priority is Online > Busy > Away; none means Offline.
func (u *userRec) recompute() {
	best := Offline
	for _, d := range u.devices {
		if d.status > best {
			best = d.status
		}
	}
	u.real = best
}

// visibleTo returns the status a viewer observes. Self always sees the real
// status; blocked viewers and viewers of an invisible user see offline.
func (u *userRec) visibleTo(viewer string) Status {
	if viewer == u.id {
		return u.real
	}
	if u.invisible || u.blocked[viewer] {
		return Offline
	}
	return u.real
}

// emit appends a visible-status change for viewer only when the newly observed
// status differs from the last notified (or subscription-initial) status.
// Expiry events are emitted per target in effective-time order, so within a
// target each later event's previous state is the earlier event's new state.
func (sb *sub) emit(target string, status Status, effective int64, seq uint64, switchEvent bool) {
	if status == sb.last {
		return
	}
	sb.pending = append(sb.pending, Notification{
		Target:      target,
		Status:      status,
		Effective:   effective,
		switchEvent: switchEvent,
		Seq:         seq,
	})
	sb.last = status
}
