package pbftlog_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/pbftlog"
)

// naiveLog is an independent, deliberately simple per-sequence reference
// implementation: maps of sender->digest and direct loops for the two
// certificates. It is driven single-threaded by the differential tests.
type naiveLog struct {
	f, n, view, primary, limit int
	executed                   int
	entries                    map[int]*naiveEntry
}

type naiveEntry struct {
	ppDigest string
	hasPP    bool
	prepares map[int]string
	commits  map[int]string
}

func newNaive(f, view, limit int) *naiveLog {
	return &naiveLog{
		f: f, n: 3*f + 1, view: view, primary: view % (3*f + 1),
		limit: limit, entries: map[int]*naiveEntry{},
	}
}

func (nl *naiveLog) e(seq int) *naiveEntry {
	e := nl.entries[seq]
	if e == nil {
		e = &naiveEntry{prepares: map[int]string{}, commits: map[int]string{}}
		nl.entries[seq] = e
	}
	return e
}

func code(err error) string {
	var r pbftlog.RejectError
	if errors.As(err, &r) {
		return string(r)
	}
	return ""
}

// handle returns the rejection-code string ("" means accepted).
func (nl *naiveLog) handle(kind pbftlog.Kind, m pbftlog.Message) string {
	if m.From < 0 || m.From >= nl.n {
		return code(pbftlog.ErrSenderOutOfRange)
	}
	if m.Digest == "" {
		return code(pbftlog.ErrEmptyDigest)
	}
	if m.View != nl.view {
		return code(pbftlog.ErrWrongView)
	}
	if !(nl.executed < m.Seq && m.Seq <= nl.executed+nl.limit) {
		return code(pbftlog.ErrSeqOutOfWindow)
	}
	if kind == pbftlog.PrePrepare && m.From != nl.primary {
		return code(pbftlog.ErrPrePrepareFromBackup)
	}
	if kind == pbftlog.Prepare && m.From == nl.primary {
		return code(pbftlog.ErrPrepareFromPrimary)
	}
	e := nl.e(m.Seq)
	switch kind {
	case pbftlog.PrePrepare:
		if e.hasPP && e.ppDigest != m.Digest {
			return code(pbftlog.ErrConflictingPrePrepare)
		}
		e.hasPP = true
		e.ppDigest = m.Digest
	case pbftlog.Prepare:
		if d, ok := e.prepares[m.From]; ok && d != m.Digest {
			return code(pbftlog.ErrConflictingPrepare)
		}
		e.prepares[m.From] = m.Digest
	case pbftlog.Commit:
		if d, ok := e.commits[m.From]; ok && d != m.Digest {
			return code(pbftlog.ErrConflictingCommit)
		}
		e.commits[m.From] = m.Digest
	}
	return ""
}

func (nl *naiveLog) prepared(seq int, digest string) bool {
	e := nl.entries[seq]
	if e == nil || !e.hasPP || e.ppDigest != digest {
		return false
	}
	c := 0
	for _, d := range e.prepares {
		if d == digest {
			c++
		}
	}
	return c >= 2*nl.f
}

func (nl *naiveLog) committedLocal(seq int, digest string) bool {
	e := nl.entries[seq]
	if e == nil || !nl.prepared(seq, digest) {
		return false
	}
	c := 0
	for _, d := range e.commits {
		if d == digest {
			c++
		}
	}
	return c >= 2*nl.f+1
}

func (nl *naiveLog) execute() []pbftlog.ExecutedEntry {
	var out []pbftlog.ExecutedEntry
	for s := nl.executed + 1; ; s++ {
		e := nl.entries[s]
		if e == nil || !e.hasPP || !nl.committedLocal(s, e.ppDigest) {
			return out
		}
		out = append(out, pbftlog.ExecutedEntry{Seq: s, Digest: e.ppDigest})
		nl.executed = s
		delete(nl.entries, s)
	}
}

type event struct {
	kind      pbftlog.Kind
	msg       pbftlog.Message
	isExecute bool
}

func TestDifferentialRandomSequences(t *testing.T) {
	const iterations = 400
	for iter := 0; iter < iterations; iter++ {
		seed := int64(1000 + iter)
		rng := rand.New(rand.NewSource(seed))
		f := 1 + rng.Intn(2) // f in {1,2}
		view := rng.Intn(3)
		limit := 1 + rng.Intn(5)
		n := 3*f + 1

		l, err := pbftlog.New(f, view, limit)
		if err != nil {
			t.Fatal(err)
		}
		nl := newNaive(f, view, limit)

		digests := []string{"", "a", "b", "c"}
		evs := make([]event, 300)
		for i := range evs {
			if (i+1)%20 == 0 {
				evs[i] = event{isExecute: true}
				continue
			}
			kind := []pbftlog.Kind{pbftlog.PrePrepare, pbftlog.Prepare, pbftlog.Commit}[rng.Intn(3)]
			seq := rng.Intn(limit+3) - 1 // -1 .. limit+1
			m := pbftlog.Message{
				View:   view,
				Seq:    seq,
				Digest: digests[rng.Intn(len(digests))],
				From:   rng.Intn(n + 2), // occasionally out of range
			}
			if rng.Intn(8) == 0 {
				m.View = view + 1 // occasionally wrong view
			}
			evs[i] = event{kind: kind, msg: m}
		}

		for i, ev := range evs {
			if ev.isExecute {
				got := l.Execute()
				want := nl.execute()
				if !entriesEqual(got, want) {
					t.Fatalf("seed=%d step=%d Execute mismatch:\n got=%v\nwant=%v", seed, i, got, want)
				}
				if l.Executed() != nl.executed {
					t.Fatalf("seed=%d step=%d executed=%d want=%d", seed, i, l.Executed(), nl.executed)
				}
				continue
			}
			gotErr := code(l.Handle(ev.kind, ev.msg))
			wantErr := nl.handle(ev.kind, ev.msg)
			if gotErr != wantErr {
				t.Fatalf("seed=%d step=%d %s %+v:\n got=%q\nwant=%q",
					seed, i, ev.kind, ev.msg, gotErr, wantErr)
			}
		}
		if got, want := l.Execute(), nl.execute(); !entriesEqual(got, want) {
			t.Fatalf("seed=%d final Execute mismatch: got=%v want=%v", seed, got, want)
		}
		if iter%50 == 0 {
			t.Logf("input seed=%d f=%d view=%d L=%d -> output executed=%d (real=naive)",
				seed, f, view, limit, l.Executed())
		}
	}
}

func entriesEqual(a, b []pbftlog.ExecutedEntry) bool {
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

// TestArrivalOrderInvariance: with the same accepted message set (no
// digest-conflicting pair per key), arbitrary arrival order must lead to the
// same executed value and execution sequence.
func TestArrivalOrderInvariance(t *testing.T) {
	for iter := 0; iter < 60; iter++ {
		seed := int64(5000 + iter)
		rng := rand.New(rand.NewSource(seed))
		f := 1 + rng.Intn(2)
		view := rng.Intn(3)
		limit := 1 + rng.Intn(4)
		n := 3*f + 1
		primary := view % n

		type op struct {
			kind pbftlog.Kind
			msg  pbftlog.Message
		}
		var msgs []op
		// Every sequence inside the window has one fixed digest; each sender
		// appears at most once per (kind, seq), possibly with an exact dup.
		for s := 1; s <= limit; s++ {
			d := fmt.Sprintf("dig-%d", rng.Intn(3))
			if rng.Intn(2) == 0 {
				m := pbftlog.Message{View: view, Seq: s, Digest: d, From: primary}
				msgs = append(msgs, op{pbftlog.PrePrepare, m})
				if rng.Intn(3) == 0 {
					msgs = append(msgs, op{pbftlog.PrePrepare, m}) // dup no-op
				}
			}
			for from := 0; from < n; from++ {
				if from == primary {
					continue
				}
				if rng.Intn(2) == 0 {
					m := pbftlog.Message{View: view, Seq: s, Digest: d, From: from}
					msgs = append(msgs, op{pbftlog.Prepare, m})
				}
			}
			for from := 0; from < n; from++ {
				if rng.Intn(2) == 0 {
					m := pbftlog.Message{View: view, Seq: s, Digest: d, From: from}
					msgs = append(msgs, op{pbftlog.Commit, m})
				}
			}
		}

		run := func(seed2 int64) (int, []pbftlog.ExecutedEntry) {
			r2 := rand.New(rand.NewSource(seed2))
			l, _ := pbftlog.New(f, view, limit)
			order := r2.Perm(len(msgs))
			for _, idx := range order {
				if err := l.Handle(msgs[idx].kind, msgs[idx].msg); err != nil {
					t.Fatalf("seed=%d unexpected reject: %v (%+v)", seed, err, msgs[idx])
				}
			}
			out := l.Execute()
			return l.Executed(), out
		}

		ex1, out1 := run(seed)
		ex2, out2 := run(seed ^ 0x5a5a)
		if ex1 != ex2 || !entriesEqual(out1, out2) {
			t.Fatalf("seed=%d order sensitivity: executed %d vs %d, out %v vs %v",
				seed, ex1, ex2, out1, out2)
		}
		t.Logf("input seed=%d f=%d L=%d msgs=%d (two shuffles) -> output executed=%d seq=%v",
			seed, f, limit, len(msgs), ex1, out1)
	}
}

func TestConcurrentHandleAndExecute(t *testing.T) {
	for _, f := range []int{1, 2} {
		t.Run(fmt.Sprintf("f=%d", f), func(t *testing.T) {
			const seqs = 6
			l, _ := pbftlog.New(f, 0, seqs)
			n := l.N()

			type op struct {
				kind pbftlog.Kind
				msg  pbftlog.Message
			}
			var ops []op
			for s := 1; s <= seqs; s++ {
				d := fmt.Sprintf("d%d", s)
				ops = append(ops, op{pbftlog.PrePrepare,
					pbftlog.Message{View: 0, Seq: s, Digest: d, From: 0}})
				for from := 1; from < n; from++ {
					ops = append(ops, op{pbftlog.Prepare,
						pbftlog.Message{View: 0, Seq: s, Digest: d, From: from}})
				}
				for from := 0; from < n; from++ {
					ops = append(ops, op{pbftlog.Commit,
						pbftlog.Message{View: 0, Seq: s, Digest: d, From: from}})
				}
			}

			var (
				wg      sync.WaitGroup
				mu      sync.Mutex
				allDone []pbftlog.ExecutedEntry
				start   = make(chan struct{})
			)
			for _, o := range ops {
				wg.Add(1)
				o := o
				go func() {
					defer wg.Done()
					<-start
					if err := l.Handle(o.kind, o.msg); err != nil {
						// A message for a high sequence can land after Execute
						// slid the window; that rejection is expected and
						// legitimate under concurrency.
						if !errors.Is(err, pbftlog.ErrSeqOutOfWindow) {
							t.Errorf("unexpected reject: %v", err)
						}
					}
				}()
			}
			for g := 0; g < 4; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for {
						out := l.Execute()
						mu.Lock()
						allDone = append(allDone, out...)
						mu.Unlock()
						if l.Executed() == seqs {
							return
						}
					}
				}()
			}
			close(start)
			wg.Wait()

			if len(allDone) != seqs {
				t.Fatalf("executed %d entries, want %d: %v", len(allDone), seqs, allDone)
			}
			for i, e := range allDone {
				if e.Seq != i+1 || e.Digest != fmt.Sprintf("d%d", i+1) {
					t.Fatalf("execution not strictly ascending 1..%d: %v", seqs, allDone)
				}
			}
			t.Logf("input concurrent %d messages + 4 Execute loops -> output executed=%d seq=%v",
				len(ops), l.Executed(), allDone)
		})
	}
}
