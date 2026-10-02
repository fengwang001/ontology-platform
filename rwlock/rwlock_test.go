package rwlock

import (
	"math/rand"
	"sync"
	"testing"
)

func mustCR(t *testing.T, co *Coordinator, sid int64, name string, kind Kind, held bool, watch int64) Result {
	t.Helper()
	r, err := co.Create(sid, name, kind)
	mustOK(t, err)
	if r.Held != held {
		t.Fatalf("create seq %d kind %c: held=%v want %v", r.Seq, kind, r.Held, held)
	}
	if !held && r.Watching != watch {
		t.Fatalf("create seq %d: watch=%d want %d", r.Seq, r.Watching, watch)
	}
	return r
}

func equal64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSequenceNeverReused(t *testing.T) {
	co := New(3)
	s := co.Open()
	var seqs []int64
	for i := 0; i < 3; i++ {
		r, err := co.Create(s, "L", Read)
		mustOK(t, err)
		seqs = append(seqs, r.Seq)
	}
	_, err := co.Create(s, "L", Read)
	if err != ErrFull {
		t.Fatalf("full: got %v want ErrFull", err)
	}
	zxAfterFull := co.zx
	_, err = co.Release(s, "L", Read, seqs[1])
	mustOK(t, err)
	r, err := co.Create(s, "L", Read)
	mustOK(t, err)
	if r.Seq != 3 {
		t.Fatalf("seq reused/skipped: got %d want 3", r.Seq)
	}
	if r.ZXID != zxAfterFull+2 {
		t.Fatalf("new create zxid = %d, want %d (rejected full create must not consume zxid)", r.ZXID, zxAfterFull+2)
	}
}

func TestReaderQueueFairness(t *testing.T) {
	co := New(100)
	s := make([]int64, 5)
	for i := range s {
		s[i] = co.Open()
	}
	mustCR(t, co, s[0], "L", Write, true, 0)
	mustCR(t, co, s[1], "L", Read, false, 0)
	mustCR(t, co, s[2], "L", Write, false, 1)
	mustCR(t, co, s[3], "L", Read, false, 2)
	mustCR(t, co, s[4], "L", Read, false, 2)

	ev, err := co.Release(s[0], "L", Write, 0)
	mustOK(t, err)
	assertGrants(t, ev, []Grant{{Lock: "L", Seq: 1, Kind: Read, Session: s[1], DeleteZX: 6}})

	ev, err = co.Release(s[1], "L", Read, 1)
	mustOK(t, err)
	assertGrants(t, ev, []Grant{{Lock: "L", Seq: 2, Kind: Write, Session: s[2], DeleteZX: 7}})

	for _, n := range co.Holders("L") {
		if n.Seq != 2 {
			t.Fatalf("only W2 may hold while pending readers wait, got %d", n.Seq)
		}
	}

	ev, err = co.Release(s[2], "L", Write, 2)
	mustOK(t, err)
	assertGrants(t, ev, []Grant{
		{Lock: "L", Seq: 3, Kind: Read, Session: s[3], DeleteZX: 8},
		{Lock: "L", Seq: 4, Kind: Read, Session: s[4], DeleteZX: 8},
	})
}

func TestReaderWatchesNearestPrecedingWriter(t *testing.T) {
	co := New(100)
	s := make([]int64, 5)
	for i := range s {
		s[i] = co.Open()
	}
	co.Create(s[0], "L", Write)
	co.Create(s[1], "L", Read)
	co.Create(s[2], "L", Read)
	w3, _ := co.Create(s[3], "L", Write)
	if w3.Watching != 2 || w3.WS != 3 {
		t.Fatalf("W3 should watch immediate predecessor R2, got %+v", w3)
	}
	r4, _ := co.Create(s[4], "L", Read)
	if r4.Watching != 3 {
		t.Fatalf("R4 watches nearest preceding W3, got %d", r4.Watching)
	}
}

func TestWriterChainNotifyCountOne(t *testing.T) {
	for _, n := range []int{100, 10000} {
		co := New(int64(n) + 10)
		sessions := make([]int64, n)
		for i := range sessions {
			sessions[i] = co.Open()
		}
		l := co.lock("L")
		co.Create(sessions[0], "L", Write)
		for i := 1; i < n; i++ {
			r, err := co.Create(sessions[i], "L", Write)
			mustOK(t, err)
			if r.Held || r.Watching != int64(i-1) {
				t.Fatalf("n=%d writer %d broken chain: %+v", n, i, r)
			}
		}
		if l.tree.size() != n {
			t.Fatalf("n=%d children=%d", n, l.tree.size())
		}
		ev, err := co.Release(sessions[0], "L", Write, 0)
		mustOK(t, err)
		if len(ev) != 1 || ev[0].Seq != 1 {
			t.Fatalf("n=%d expected exactly one grant (seq 1), got %v", n, ev)
		}
		if l.lastReevals != 1 {
			t.Fatalf("n=%d re-evaluations = %d, want 1", n, l.lastReevals)
		}
		if l.watchers[0] != nil || len(l.watchers[1]) != 1 {
			t.Fatalf("n=%d watcher structure wrong", n)
		}
	}
}

func TestCancelRepointsToEarlierWriter(t *testing.T) {
	co := New(100)
	s := make([]int64, 5)
	for i := range s {
		s[i] = co.Open()
	}
	co.Create(s[0], "L", Write)
	co.Create(s[1], "L", Read)
	w2, _ := co.Create(s[2], "L", Write)
	co.Create(s[3], "L", Read)
	ev, err := co.Release(s[2], "L", Write, w2.Seq)
	mustOK(t, err)
	if len(ev) != 0 {
		t.Fatalf("cancel grants nothing, got %v", ev)
	}
	l := co.lock("L")
	r3 := l.byS[3]
	if r3.watch != 0 || r3.ws != 4 {
		t.Fatalf("R3 should repoint to W0 with ws 4, got watch=%d ws=%d", r3.watch, r3.ws)
	}
	if len(l.watchers[0]) != 2 || l.watchers[0][0].seq != 1 || l.watchers[0][1].seq != 3 {
		t.Fatalf("watchers of 0: %+v", l.watchers[0])
	}
}

func TestReRegistrationWSOrder(t *testing.T) {
	co := New(100)
	s := make([]int64, 6)
	for i := range s {
		s[i] = co.Open()
	}
	co.Create(s[0], "L", Write)
	co.Create(s[1], "L", Read)
	co.Create(s[2], "L", Read)
	co.Create(s[3], "L", Write)
	co.Create(s[4], "L", Read)
	co.Create(s[5], "L", Read)

	co.Release(s[3], "L", Write, 3)
	l := co.lock("L")
	var gotSeqs []int64
	for _, w := range l.watchers[0] {
		gotSeqs = append(gotSeqs, w.seq)
	}
	want := []int64{1, 2, 4, 5}
	if !equal64(gotSeqs, want) {
		t.Fatalf("ws order on 0 = %v want %v", gotSeqs, want)
	}

	ev, err := co.Release(s[0], "L", Write, 0)
	mustOK(t, err)
	if len(ev) != 4 {
		t.Fatalf("grants = %v", ev)
	}
	for i, seq := range want {
		if ev[i].Seq != seq || ev[i].DeleteZX != 8 {
			t.Fatalf("grant[%d] = %+v, want seq %d zx 8", i, ev[i], seq)
		}
	}
}

func TestExpireOrderAndNoSelfGrants(t *testing.T) {
	co := New(100)
	a, b := co.Open(), co.Open()
	co.Create(a, "L", Write)
	co.Create(b, "L", Write)
	r2, _ := co.Create(a, "L", Read)
	if r2.Held || r2.Watching != 1 {
		t.Fatalf("R2 setup: %+v", r2)
	}

	ev, err := co.Expire(a)
	mustOK(t, err)
	assertGrants(t, ev, []Grant{
		{Lock: "L", Seq: 1, Kind: Write, Session: b, DeleteZX: 4},
	})
	l := co.lock("L")
	if _, ok := l.byS[0]; ok {
		t.Fatal("W0 not removed")
	}
	if _, ok := l.byS[2]; ok {
		t.Fatal("R2 of expired session not removed")
	}
	w1 := l.byS[1]
	if w1 == nil || !w1.held {
		t.Fatal("b's W1 should now hold")
	}

	if _, err := co.Expire(a); err != ErrExpired {
		t.Fatalf("second expire: got %v want ErrExpired", err)
	}
	if _, err := co.Create(a, "L", Read); err != ErrExpired {
		t.Fatalf("create on expired: got %v", err)
	}
}

func TestRejectionPrecedenceAndNoStateChange(t *testing.T) {
	co := New(1)
	s := co.Open()

	checkErr := func(got, want error) {
		t.Helper()
		if got != want {
			t.Fatalf("got %v want %v", got, want)
		}
	}

	_, err := co.Create(s, "", Read)
	checkErr(err, ErrInvalid)
	_, err = co.Create(s, "L", Kind('X'))
	checkErr(err, ErrInvalid)
	_, err = co.Create(999, "L", Read)
	checkErr(err, ErrNoSession)
	co.Expire(s)
	_, err = co.Create(s, "L", Read)
	checkErr(err, ErrExpired)

	s2 := co.Open()
	co.Create(s2, "L", Read)
	_, err = co.Create(s2, "L", Read)
	checkErr(err, ErrFull)

	zx, ws := co.zx, co.ws
	cs := co.lock("L").cs

	_, err = co.Release(s2, "", Read, 0)
	checkErr(err, ErrInvalid)
	_, err = co.Release(s2, "L", Kind('X'), 0)
	checkErr(err, ErrInvalid)
	_, err = co.Release(s2, "L", Read, -1)
	checkErr(err, ErrInvalid)
	_, err = co.Release(998, "L", Read, 0)
	checkErr(err, ErrNoSession)
	_, err = co.Release(s, "L", Read, 0)
	checkErr(err, ErrExpired)
	_, err = co.Release(s2, "nope", Read, 0)
	checkErr(err, ErrNoNode)
	_, err = co.Release(s2, "L", Read, 7)
	checkErr(err, ErrNoNode)

	s3 := co.Open()
	_, err = co.Release(s3, "L", Read, 0)
	checkErr(err, ErrNotOwner)

	_, err = co.Expire(997)
	checkErr(err, ErrNoSession)

	if co.zx != zx || co.ws != ws || co.lock("L").cs != cs {
		t.Fatalf("rejected op changed counters: zx %d->%d ws %d->%d cs %d->%d",
			zx, co.zx, ws, co.ws, cs, co.lock("L").cs)
	}
}

func checkGlobalInvariants(t *testing.T, co *Coordinator) {
	t.Helper()
	for name, l := range co.locks {
		var heldKinds []Kind
		waiters := 0
		watchTotal := 0
		seenWS := map[int64]bool{}
		for _, c := range l.byS {
			if c.held {
				heldKinds = append(heldKinds, c.kind)
			} else {
				waiters++
				if c.watch < 0 || l.byS[c.watch] == nil {
					t.Fatalf("lock %s waiter %d watches missing node %d", name, c.seq, c.watch)
				}
				if c.ws <= 0 || seenWS[c.ws] {
					t.Fatalf("waiter %d bad/dup ws %d", c.seq, c.ws)
				}
				seenWS[c.ws] = true
			}
		}
		nW, nR := 0, 0
		for _, k := range heldKinds {
			if k == Write {
				nW++
			} else {
				nR++
			}
		}
		if nW > 1 || (nW == 1 && nR > 0) {
			t.Fatalf("lock %s holders violate R/W exclusion: %dW %dR", name, nW, nR)
		}
		seqs := make([]int64, 0, len(l.byS))
		for seq := range l.watchers {
			list := l.watchers[seq]
			watchTotal += len(list)
			for j := 1; j < len(list); j++ {
				if list[j-1].ws >= list[j].ws {
					t.Fatalf("lock %s watchers of %d not ws-sorted", name, seq)
				}
			}
		}
		for seq := range l.byS {
			seqs = append(seqs, seq)
		}
		if watchTotal != waiters {
			t.Fatalf("lock %s watch total %d != waiters %d (nodes %v)", name, watchTotal, waiters, seqs)
		}
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	co := New(100000)
	const sessions = 16
	for i := 0; i < sessions; i++ {
		co.Open()
	}
	var wg sync.WaitGroup
	for g := 0; g < sessions; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 7))
			for i := 0; i < 2000; i++ {
				sid := int64(1 + rng.Intn(sessions))
				name := []string{"A", "B", "C"}[rng.Intn(3)]
				kind := Read
				if rng.Intn(2) == 0 {
					kind = Write
				}
				r, err := co.Create(sid, name, kind)
				if err == nil && rng.Intn(3) == 0 {
					_, _ = co.Release(sid, name, kind, r.Seq)
				}
				_ = co.Holders(name)
				_ = co.Children(name)
			}
		}(g)
	}
	wg.Wait()
	co.mu.Lock()
	checkGlobalInvariants(t, co)
	co.mu.Unlock()
}

func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		co := New(10)
		s := make([]int64, 6)
		for i := range s {
			s[i] = co.Open()
		}
		var log []string
		ops := []func(){
			func() { r, _ := co.Create(s[0], "L", Write); log = append(log, fmtResult(r)) },
			func() { r, _ := co.Create(s[1], "L", Read); log = append(log, fmtResult(r)) },
			func() { r, _ := co.Create(s[2], "L", Read); log = append(log, fmtResult(r)) },
			func() { r, _ := co.Create(s[3], "L", Write); log = append(log, fmtResult(r)) },
			func() { r, _ := co.Create(s[4], "L", Read); log = append(log, fmtResult(r)) },
			func() { ev, _ := co.Release(s[3], "L", Write, 3); log = append(log, fmtGrants(ev)) },
			func() { ev, _ := co.Release(s[0], "L", Write, 0); log = append(log, fmtGrants(ev)) },
			func() { r, _ := co.Create(s[5], "L", Write); log = append(log, fmtResult(r)) },
		}
		for _, op := range ops {
			op()
		}
		return log
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d:\n%s\nvs\n%s", i, first[i], second[i])
		}
	}
}
