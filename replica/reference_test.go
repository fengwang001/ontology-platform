package replica

import (
	"math/rand"
	"testing"
	"time"
)

// refState 是与实现无关的朴素参照：直接按题目规则用 map 维护，
// 每次操作整体校验、通过后才提交（失败不留痕）。
type refState struct {
	leader    string
	tolerance time.Duration
	clock     time.Time
	ends      map[string]int64
	caught    map[string]time.Time
	synced    map[string]bool
	order     []string
	hwm       int64
}

func newRef(leader string, tol time.Duration, start time.Time) *refState {
	return &refState{
		leader: leader, tolerance: tol, clock: start,
		ends:   map[string]int64{leader: 0},
		caught: map[string]time.Time{leader: start},
		synced: map[string]bool{leader: true},
		order:  []string{leader},
	}
}

func (r *refState) recomputeHWM() {
	min := r.ends[r.leader]
	for id := range r.synced {
		if r.ends[id] < min {
			min = r.ends[id]
		}
	}
	if min > r.hwm {
		r.hwm = min
	}
}

func (r *refState) add(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	if _, ok := r.ends[id]; ok {
		return ErrInvalidArgument
	}
	r.order = append(r.order, id)
	r.ends[id] = 0
	delete(r.synced, id)
	return nil
}

func (r *refState) append(now time.Time, n int64) error {
	if n < 0 || now.IsZero() {
		return ErrInvalidArgument
	}
	if now.Before(r.clock) {
		return ErrClockRollback
	}
	newEnd := r.ends[r.leader] + n
	if newEnd < r.ends[r.leader] {
		return ErrInvalidArgument
	}
	r.ends[r.leader] = newEnd
	r.caught[r.leader] = now
	r.clock = now
	r.recomputeHWM()
	return nil
}

func (r *refState) fetch(now time.Time, id string, end int64) error {
	// 与实现相同的校验顺序。
	if id == "" || now.IsZero() {
		return ErrInvalidArgument
	}
	if _, ok := r.ends[id]; !ok {
		return ErrUnknownReplica
	}
	if id == r.leader {
		return ErrInvalidArgument
	}
	if now.Before(r.clock) {
		return ErrClockRollback
	}
	if end < 0 || end > r.ends[r.leader] || end < r.ends[id] {
		return ErrInvalidOffset
	}
	r.ends[id] = end
	caughtUp := end == r.ends[r.leader]
	if caughtUp {
		r.caught[id] = now
	}
	if !r.synced[id] && caughtUp && end >= r.hwm {
		r.synced[id] = true
	}
	r.clock = now
	r.recomputeHWM()
	return nil
}

func (r *refState) sweep(now time.Time) ([]string, error) {
	if now.IsZero() {
		return nil, ErrInvalidArgument
	}
	if now.Before(r.clock) {
		return nil, ErrClockRollback
	}
	r.clock = now
	var removed []string
	limit := now.Add(-r.tolerance)
	for _, id := range r.order {
		if id == r.leader || !r.synced[id] {
			continue
		}
		if r.caught[id].Before(limit) {
			delete(r.synced, id)
			removed = append(removed, id)
		}
	}
	r.recomputeHWM()
	return removed, nil
}

func (r *refState) status() Status {
	st := Status{
		Leader:        r.leader,
		HighWatermark: r.hwm,
		LastClock:     r.clock,
		Ends:          map[string]int64{},
	}
	for _, id := range r.order {
		if r.synced[id] {
			st.Synced = append(st.Synced, id)
		}
		st.Ends[id] = r.ends[id]
	}
	return st
}

func TestMatchesNaiveReference(t *testing.T) {
	const tol = 5 * time.Second
	t0 := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	s, err := NewSyncSet("L", tol, t0, WithLogWriter(discardWriter{}))
	if err != nil {
		t.Fatal(err)
	}
	ref := newRef("L", tol, t0)

	followers := []string{"a", "b", "c", "d"}
	for _, id := range followers {
		if err := s.AddReplica(id); err != nil {
			t.Fatal(err)
		}
		if err := ref.add(id); err != nil {
			t.Fatal(err)
		}
	}

	rng := rand.New(rand.NewSource(315315))
	clock := t0
	match := func(step int) {
		t.Helper()
		got := s.Status()
		want := ref.status()
		if !statusEqual(got, want) {
			t.Fatalf("step %d mismatch:\n got=%+v\nwant=%+v", step, got, want)
		}
	}

	for step := 0; step < 3000; step++ {
		clock = clock.Add(time.Duration(1+rng.Intn(3)) * time.Second)
		switch rng.Intn(6) {
		case 0, 1: // append
			n := int64(rng.Intn(4))
			if rng.Intn(10) == 0 {
				n = -1
			}
			now := clock
			if rng.Intn(20) == 0 {
				now = t0 // 注入时钟回退/非法时间
			}
			_, _, e1 := s.Append(now, n)
			e2 := ref.append(now, n)
			if !sameErr(e1, e2) {
				t.Fatalf("step %d append(%v,%d): impl=%v ref=%v", step, now, n, e1, e2)
			}
		case 2, 3, 4: // fetch
			id := followers[rng.Intn(len(followers))]
			cur := ref.ends[id]
			leaderEnd := ref.ends["L"]
			var end int64
			switch rng.Intn(6) {
			case 0:
				end = cur + 1 + rng.Int63n(3) // 可能超前
				if end <= leaderEnd {
					end = leaderEnd + 1 // 强制构造一个确定的超前值
				}
			case 1:
				end = cur - 1 // 回退（cur==0 时为负）
			default:
				end = cur + rng.Int63n(leaderEnd-cur+1)
			}
			now := clock
			if rng.Intn(30) == 0 {
				now = t0
			}
			e1 := s.Fetch(now, id, end)
			e2 := ref.fetch(now, id, end)
			if !sameErr(e1, e2) {
				t.Fatalf("step %d fetch(%v,%q,%d): impl=%v ref=%v", step, now, id, end, e1, e2)
			}
		case 5: // sweep
			now := clock
			if rng.Intn(30) == 0 {
				now = t0
			}
			r1, e1 := s.Sweep(now)
			r2, e2 := ref.sweep(now)
			if !sameErr(e1, e2) {
				t.Fatalf("step %d sweep(%v): impl=%v ref=%v", step, now, e1, e2)
			}
			if len(r1) != len(r2) {
				t.Fatalf("step %d sweep removed impl=%v ref=%v", step, r1, r2)
			}
			for i := range r1 {
				if r1[i] != r2[i] {
					t.Fatalf("step %d sweep removed impl=%v ref=%v", step, r1, r2)
				}
			}
		}
		match(step)
	}

	// 收敛：把所有跟随者拉满，两边最终 hwm 必须都等于领导者位点。
	leaderEnd := ref.ends["L"]
	for _, id := range followers {
		for ref.ends[id] < leaderEnd {
			clock = clock.Add(time.Second)
			next := ref.ends[id] + 1
			if err := s.Fetch(clock, id, next); err != nil {
				t.Fatal(err)
			}
			if err := ref.fetch(clock, id, next); err != nil {
				t.Fatal(err)
			}
		}
	}
	match(3000)
	if ref.hwm != leaderEnd {
		t.Fatalf("reference hwm %d != %d", ref.hwm, leaderEnd)
	}
}

func sameErr(a, b error) bool { return a == b }

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
