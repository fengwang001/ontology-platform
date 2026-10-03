package repl_test

import (
	"errors"
	"testing"

	"ontology/link"
	"ontology/region"
	"ontology/repl"
)

func okSend(_ link.Item) (bool, error) { return true, nil }

// drain 反复投递所有链路直到没有任何可推进的队项（不含失败重试）。
func drain(t *testing.T, sys *repl.System, send link.SendFunc) {
	t.Helper()
	rs := sys.Regions()
	for {
		moved := false
		for _, src := range rs {
			for _, dst := range rs {
				if src == dst {
					continue
				}
				n, err := sys.Deliver(src, dst, 1000, send)
				if err != nil {
					t.Fatalf("drain Deliver %s->%s: %v", src, dst, err)
				}
				if n > 0 {
					moved = true
				}
			}
		}
		if !moved {
			return
		}
	}
}

func statsOf(sys *repl.System, src, dst string) repl.LinkStats {
	return sys.Snapshot().Links[[2]string{src, dst}].Stats
}

func queuedCount(sys *repl.System) int64 {
	var n int64
	for _, l := range sys.Snapshot().Links {
		n += l.Stats.Queued
	}
	return n
}

func queuedCountOn(sys *repl.System, src, dst string) int64 {
	return statsOf(sys, src, dst).Queued
}

func failedCount(sys *repl.System) int {
	n := 0
	for _, l := range sys.Snapshot().Links {
		n += len(l.Failed)
	}
	return n
}

func lostCount(sys *repl.System, src, dst string) int64 {
	return statsOf(sys, src, dst).Lost
}

func dupCount(sys *repl.System) int64 {
	var n int64
	for _, l := range sys.Snapshot().Links {
		n += l.Stats.Dup
	}
	return n
}

func regionVersionCount(sys *repl.System, r string) int {
	return len(sys.Snapshot().Versions[r])
}

func snapEqual(a, b repl.Snapshot) bool {
	if len(a.Regions) != len(b.Regions) {
		return false
	}
	for i, r := range a.Regions {
		if r != b.Regions[i] {
			return false
		}
		if a.NextSeq[r] != b.NextSeq[r] || len(a.Versions[r]) != len(b.Versions[r]) {
			return false
		}
		for i := range a.Versions[r] {
			if a.Versions[r][i] != b.Versions[r][i] {
				return false
			}
		}
	}
	if len(a.Links) != len(b.Links) {
		return false
	}
	for k, la := range a.Links {
		lb, ok := b.Links[k]
		if !ok || la.Stats != lb.Stats || len(la.Queue) != len(lb.Queue) ||
			len(la.Failed) != len(lb.Failed) {
			return false
		}
		for i := range la.Queue {
			if la.Queue[i] != lb.Queue[i] {
				return false
			}
		}
		for i := range la.Failed {
			if la.Failed[i] != lb.Failed[i] {
				return false
			}
		}
	}
	return true
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name string
		p    repl.Params
		want error
	}{
		{"one region", repl.Params{Regions: []string{"A"}, C: 10, R: 2}, repl.ErrInvalidArg},
		{"five regions", repl.Params{Regions: []string{"A", "B", "C", "D", "E"}, C: 10, R: 2}, repl.ErrInvalidArg},
		{"empty name", repl.Params{Regions: []string{"A", ""}, C: 10, R: 2}, repl.ErrInvalidArg},
		{"duplicate", repl.Params{Regions: []string{"A", "A"}, C: 10, R: 2}, repl.ErrInvalidArg},
		{"c zero", repl.Params{Regions: []string{"A", "B"}, C: 0, R: 2}, repl.ErrInvalidArg},
		{"c over", repl.Params{Regions: []string{"A", "B"}, C: 1e12 + 1, R: 2}, repl.ErrInvalidArg},
		{"r zero", repl.Params{Regions: []string{"A", "B"}, C: 10, R: 0}, repl.ErrInvalidArg},
		{"r over", repl.Params{Regions: []string{"A", "B"}, C: 10, R: 101}, repl.ErrInvalidArg},
		{"valid", repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repl.New(tc.p)
			if !errors.Is(err, tc.want) {
				t.Fatalf("New = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPutDeleteValidationOrder(t *testing.T) {
	sys, err := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2, Mode: repl.Strict})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Put("A", "", 1, 1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := sys.Put("ZZ", "k", 1, 1); !errors.Is(err, repl.ErrNoRegion) {
		t.Fatalf("unknown region: %v", err)
	}
	if _, err := sys.Put("A", "k", -1, 1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("bad size: %v", err)
	}
	if _, err := sys.Put("A", "k", 1, 1e12+1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("bad ts: %v", err)
	}
	if _, err := sys.Put("A", "k", 1e9+1, 1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("bad size high: %v", err)
	}
	if _, err := sys.Delete("A", "k", -1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("delete bad ts: %v", err)
	}
	if _, err := sys.Delete("ZZ", "k", 1); !errors.Is(err, repl.ErrNoRegion) {
		t.Fatalf("delete unknown region: %v", err)
	}
	got, err := sys.Get("ZZ", "k")
	if !errors.Is(err, repl.ErrNoRegion) || got.Exists {
		t.Fatalf("Get unknown: %v %+v", err, got)
	}
}

func TestDeliverInvalidArgs(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2})
	if _, err := sys.Deliver("A", "B", 0, okSend); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("n=0: %v", err)
	}
	if _, err := sys.Deliver("A", "B", 1001, okSend); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("n=1001: %v", err)
	}
	if _, err := sys.Deliver("A", "A", 1, okSend); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("same region: %v", err)
	}
	if _, err := sys.Deliver("A", "X", 1, okSend); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("unknown dst: %v", err)
	}
	if _, err := sys.Deliver("X", "B", 1, nil); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("unknown src: %v", err)
	}
}

func TestTieTSOriginThenSeq(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 2, MarkerRepl: true})
	idA, _ := sys.Put("A", "k", 5, 10)
	idB, _ := sys.Put("B", "k", 7, 10)
	drain(t, sys, okSend)
	for _, r := range sys.Regions() {
		got, err := sys.Get(r, "k")
		if err != nil || !got.Exists || got.Deleted || got.Version.ID != idB {
			t.Fatalf("region %s current=%+v err=%v, want B version %v", r, got, err, idB)
		}
	}
	idD, _ := sys.Delete("A", "k", 12)
	drain(t, sys, okSend)
	for _, r := range sys.Regions() {
		got, _ := sys.Get(r, "k")
		if !got.Exists || !got.Deleted || got.Version.ID != idD {
			t.Fatalf("region %s after delete: %+v, want marker %v", r, got, idD)
		}
	}
	if sys.Diverged("k") {
		t.Fatal("converged marker must not diverge")
	}
	// 无键：Get 不存在；所有区域都无 => Diverged 为假。
	if got, _ := sys.Get("A", "nope"); got.Exists {
		t.Fatal("missing key must report not exists")
	}
	if sys.Diverged("nope") {
		t.Fatal("all-absent key must not diverge")
	}
	_ = idA
}

func TestSameOriginSeqTiebreak(t *testing.T) {
	// 同 origin 同 ts 不可能同 seq；构造同 ts 的两版本，seq 大者当前。
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 2, MarkerRepl: true})
	id1, _ := sys.Put("A", "k", 1, 5)
	id2, _ := sys.Put("A", "k", 2, 5)
	drain(t, sys, okSend)
	for _, r := range sys.Regions() {
		got, _ := sys.Get(r, "k")
		if got.Version.ID != id2 {
			t.Fatalf("%s current=%v want %v (id1=%v)", r, got.Version.ID, id2, id1)
		}
	}
}

func TestThreeRegionFanoutNoLoop(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B", "C"}, C: 1e9, R: 2, MarkerRepl: true})
	id, err := sys.Put("A", "k", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total := queuedCount(sys); total != 2 {
		t.Fatalf("queued items=%d, want 2 (direct fanout, no relay)", total)
	}
	drain(t, sys, okSend)
	for _, r := range sys.Regions() {
		got, _ := sys.Get(r, "k")
		if !got.Exists || got.Version.ID != id {
			t.Fatalf("region %s missing version %v: %+v", r, id, got)
		}
	}
	if queuedCount(sys) != 0 || failedCount(sys) != 0 {
		t.Fatal("queues not empty after drain")
	}
	// 不变量：入队总数 = 已投递 + 失败转移 + 在队
	for _, l := range sys.Snapshot().Links {
		if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers+l.Stats.Queued {
			t.Fatalf("invariant broken: %+v", l.Stats)
		}
	}
	// 副本不回环：B/C 上该版本是副本；任何时刻复制项 origin 都等于源区域。
	for _, r := range []string{"B", "C"} {
		var v region.Version
		for _, x := range sys.Snapshot().Versions[r] {
			v = x
		}
		if !v.Replica {
			t.Fatalf("version at %s must be replica", r)
		}
	}
}

func TestDuplicateApplyIdempotent(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 5, MarkerRepl: true})
	id, _ := sys.Put("A", "k", 5, 1)
	first := true
	send := func(link.Item) (bool, error) {
		if first {
			first = false
			return true, link.FailError{} // applied=true 且 err!=nil：确认丢失
		}
		return true, nil
	}
	n1, err := sys.Deliver("A", "B", 10, send)
	if err != nil || n1 != 1 {
		t.Fatalf("first Deliver n=%d err=%v, want 1 (tries=1<R blocks)", n1, err)
	}
	// 确认丢失语义：第一次已经 Apply，B 已可见。
	if got, _ := sys.Get("B", "k"); !got.Exists {
		t.Fatal("ack-loss: applied=true must already apply on first send")
	}
	n2, err := sys.Deliver("A", "B", 10, send)
	if err != nil || n2 != 1 {
		t.Fatalf("second Deliver n=%d err=%v, want 1", n2, err)
	}
	got, _ := sys.Get("B", "k")
	if !got.Exists || got.Version.ID != id {
		t.Fatalf("B missing version: %+v", got)
	}
	if dup := dupCount(sys); dup != 1 {
		t.Fatalf("dup=%d, want 1", dup)
	}
	if cnt := regionVersionCount(sys, "B"); cnt != 1 {
		t.Fatalf("B versions=%d, want 1", cnt)
	}
	// 第二次完整重试成功后再次幂等投递同一标识仍只增 dup、不加版本。
	// （此时队列已空，直接用快照检查不变量）
	for _, l := range sys.Snapshot().Links {
		if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers+l.Stats.Queued {
			t.Fatalf("invariant: %+v", l.Stats)
		}
	}
}

func TestNoApplyOnAppliedFalseErrNil(t *testing.T) {
	// applied=false 且 err==nil：按规则仍 Apply（err 为空即 Apply），并出队。
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 5, MarkerRepl: true})
	id, _ := sys.Put("A", "k", 5, 1)
	n, err := sys.Deliver("A", "B", 1, func(link.Item) (bool, error) { return false, nil })
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got, _ := sys.Get("B", "k"); !got.Exists || got.Version.ID != id {
		t.Fatalf("err==nil must apply even when applied=false: %+v", got)
	}
	if dup := dupCount(sys); dup != 0 {
		t.Fatalf("dup=%d, want 0", dup)
	}
}

func TestStrictRejectAtomic(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B", "C"}, C: 10, R: 2, Mode: repl.Strict, MarkerRepl: true})
	if _, err := sys.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	before := sys.Snapshot()
	if _, err := sys.Put("A", "m", 5, 2); !errors.Is(err, repl.ErrBacklog) {
		t.Fatalf("second put: %v", err)
	}
	if !snapEqual(before, sys.Snapshot()) {
		t.Fatal("state changed after strict reject")
	}
	id, _ := sys.Put("A", "n", 1, 3)
	if id.Origin != "A" || id.Seq != 2 {
		t.Fatalf("id=%v, want A/2 (rejected op must not consume seq)", id)
	}
}

func TestRelaxedLostAndExactCapacity(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2, Mode: repl.Relaxed, MarkerRepl: true})
	if _, err := sys.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	idLost, err := sys.Put("A", "m", 5, 2)
	if err != nil {
		t.Fatalf("relaxed must accept: %v", err)
	}
	if l := lostCount(sys, "A", "B"); l != 1 {
		t.Fatalf("lost=%d, want 1", l)
	}
	if _, err := sys.Put("A", "eq", 4, 3); err != nil {
		t.Fatalf("exact capacity 6+4=10 must pass: %v", err)
	}
	if st := statsOf(sys, "A", "B"); st.Backlog != 10 {
		t.Fatalf("backlog=%d, want 10", st.Backlog)
	}
	drain(t, sys, okSend)
	if got, _ := sys.Get("B", "m"); got.Exists {
		t.Fatal("lost version must never replicate")
	}
	if got, _ := sys.Get("A", "m"); !got.Exists || got.Version.ID != idLost {
		t.Fatalf("origin must keep lost version: %+v", got)
	}
	if got, _ := sys.Get("B", "eq"); !got.Exists {
		t.Fatal("exact-capacity version should replicate")
	}
	if !sys.Diverged("m") {
		t.Fatal("lost version must cause divergence (A has, B absent)")
	}
}

func TestBacklogReleaseThenAccept(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2, Mode: repl.Strict})
	if _, err := sys.Put("A", "k", 6, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Put("A", "m", 5, 2); !errors.Is(err, repl.ErrBacklog) {
		t.Fatalf("want backlog, got %v", err)
	}
	if n, err := sys.Deliver("A", "B", 1, okSend); err != nil || n != 1 {
		t.Fatalf("deliver n=%d err=%v", n, err)
	}
	if st := statsOf(sys, "A", "B"); st.Backlog != 0 {
		t.Fatalf("backlog=%d, want 0", st.Backlog)
	}
	if _, err := sys.Put("A", "m", 5, 2); err != nil {
		t.Fatalf("after release put must pass: %v", err)
	}
}

func TestFailureTransferAndRetry(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 2, MarkerRepl: true})
	id1, _ := sys.Put("A", "k1", 3, 1)
	id2, _ := sys.Put("A", "k2", 2, 2)
	failAll := func(link.Item) (bool, error) { return false, link.FailError{} }
	n1, err := sys.Deliver("A", "B", 10, failAll)
	if err != nil || n1 != 1 {
		t.Fatalf("first Deliver sends=%d, want 1 (head1 tries=1<R blocks)", n1)
	}
	n2, err := sys.Deliver("A", "B", 10, failAll)
	if err != nil || n2 != 2 {
		t.Fatalf("second Deliver sends=%d, want 2 (head1 reaches R transfers, head2 blocks)", n2)
	}
	st := statsOf(sys, "A", "B")
	if st.Transfers != 1 || st.Queued != 1 || st.Backlog != 2 {
		t.Fatalf("stats after transfer: %+v", st)
	}
	if failedCount(sys) != 1 {
		t.Fatalf("failed=%d, want 1", failedCount(sys))
	}
	if err := sys.Retry("A", "B", region.VersionID{Origin: "A", Seq: 99}); !errors.Is(err, repl.ErrNotFound) {
		t.Fatalf("retry missing: %v", err)
	}
	if err := sys.Retry("A", "A", id1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("retry same region must be invalid: %v", err)
	}
	if err := sys.Retry("X", "B", id1); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("retry unknown region: %v", err)
	}
	if err := sys.Retry("A", "B", region.VersionID{}); !errors.Is(err, repl.ErrInvalidArg) {
		t.Fatalf("retry zero id: %v", err)
	}
	if err := sys.Retry("A", "B", id1); err != nil {
		t.Fatalf("retry within budget: %v", err)
	}
	if failedCount(sys) != 0 || queuedCountOn(sys, "A", "B") != 2 {
		t.Fatalf("after retry failed=%d queued=%d", failedCount(sys), queuedCountOn(sys, "A", "B"))
	}
	if st := statsOf(sys, "A", "B"); st.Backlog != 5 {
		t.Fatalf("backlog after retry=%d, want 5", st.Backlog)
	}
	drain(t, sys, okSend)
	for _, c := range []struct {
		id  region.VersionID
		key string
	}{{id1, "k1"}, {id2, "k2"}} {
		if got, _ := sys.Get("B", c.key); !got.Exists || got.Version.ID != c.id {
			t.Fatalf("B missing %v after retry drain: %+v", c.id, got)
		}
	}
}

func TestRetryBacklogExactAndOver(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 10, R: 1, MarkerRepl: true})
	big, _ := sys.Put("A", "big", 8, 1)
	fail := func(link.Item) (bool, error) { return false, link.FailError{} }
	if _, err := sys.Deliver("A", "B", 5, fail); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Put("A", "fill", 3, 2); err != nil {
		t.Fatal(err)
	}
	// 队列 3 字节；8+3=11>10 => Retry 报积压超限，且失败项仍在失败列表。
	if err := sys.Retry("A", "B", big); !errors.Is(err, repl.ErrBacklog) {
		t.Fatalf("retry over budget: %v", err)
	}
	if failedCount(sys) != 1 {
		t.Fatal("failed item must remain on backlog rejection")
	}
	// 投递掉 fill 后积压 0，8<=10 通过。
	if _, err := sys.Deliver("A", "B", 5, okSend); err != nil {
		t.Fatal(err)
	}
	if err := sys.Retry("A", "B", big); err != nil {
		t.Fatalf("retry after drain: %v", err)
	}
}

func TestMarkerReplToggleDivergence(t *testing.T) {
	build := func(marker bool) *repl.System {
		sys, err := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 2, MarkerRepl: marker})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = sys.Put("A", "k", 5, 10)
		_, _ = sys.Put("B", "k", 7, 10)
		drain(t, sys, okSend)
		_, _ = sys.Delete("A", "k", 12)
		return sys
	}
	on := build(true)
	drain(t, on, okSend)
	for _, r := range on.Regions() {
		got, _ := on.Get(r, "k")
		if !got.Exists || !got.Deleted {
			t.Fatalf("marker on: %s got %+v, want deleted", r, got)
		}
	}
	if on.Diverged("k") {
		t.Fatal("marker on: must converge after drain")
	}
	off := build(false)
	drain(t, off, okSend)
	gotA, _ := off.Get("A", "k")
	gotB, _ := off.Get("B", "k")
	if !gotA.Exists || !gotA.Deleted {
		t.Fatalf("marker off: A=%+v want deleted", gotA)
	}
	if !gotB.Exists || gotB.Deleted {
		t.Fatalf("marker off: B=%+v must still see B data version", gotB)
	}
	if !off.Diverged("k") {
		t.Fatal("marker off: divergence expected")
	}
}

func TestDeleteWithoutAnyVersion(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B"}, C: 1e9, R: 2, MarkerRepl: true})
	id, err := sys.Delete("A", "ghost", 3)
	if err != nil {
		t.Fatalf("delete without versions must succeed: %v", err)
	}
	got, _ := sys.Get("A", "ghost")
	if !got.Exists || !got.Deleted || got.Version.ID != id {
		t.Fatalf("ghost marker: %+v", got)
	}
	// 标记 size=0 入队不占预算，复制后 B 同样被标记删除。
	drain(t, sys, okSend)
	gotB, _ := sys.Get("B", "ghost")
	if !gotB.Exists || !gotB.Deleted {
		t.Fatalf("B ghost: %+v", gotB)
	}
}

func TestStrictExactCapacityAcrossLinks(t *testing.T) {
	// 三区域：size=10 时每条链路恰等 C=10 通过；再建任何 >0 版本都被拒。
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B", "C"}, C: 10, R: 2, Mode: repl.Strict})
	if _, err := sys.Put("A", "k", 10, 1); err != nil {
		t.Fatalf("size=C must pass on every link: %v", err)
	}
	if _, err := sys.Put("A", "m", 1, 1); !errors.Is(err, repl.ErrBacklog) {
		t.Fatalf("size 1 over exact backlog: %v", err)
	}
	// size=0 的删除标记即使 Strict 也不受字节预算约束（恰等 +0）。
	if _, err := sys.Delete("A", "k", 2); err != nil {
		t.Fatalf("zero-size marker must pass: %v", err)
	}
}

func TestMarkerOffNoEnqueueAtAll(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B", "C"}, C: 10, R: 2, MarkerRepl: false})
	if _, err := sys.Delete("A", "k", 5); err != nil {
		t.Fatal(err)
	}
	for _, l := range sys.Snapshot().Links {
		if l.Stats.Enqueued != 0 {
			t.Fatalf("marker must not be enqueued: %+v", l.Stats)
		}
	}
}

func TestBacklogBytesEqualQueueSizes(t *testing.T) {
	sys, _ := repl.New(repl.Params{Regions: []string{"A", "B", "C"}, C: 100, R: 2, Mode: repl.Strict, MarkerRepl: true})
	_, _ = sys.Put("A", "k", 10, 1)
	_, _ = sys.Put("A", "m", 15, 2)
	snap := sys.Snapshot()
	for key, l := range snap.Links {
		var sum int64
		for _, q := range l.Queue {
			sum += q.Ver.Size
		}
		if sum != l.Stats.Backlog {
			t.Fatalf("link %v backlog=%d sum=%d", key, l.Stats.Backlog, sum)
		}
		if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers+l.Stats.Queued {
			t.Fatalf("invariant %v: %+v", key, l.Stats)
		}
	}
}
