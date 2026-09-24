package check

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/drr"
)

func TestDRR(t *testing.T) {
	sameErr := func(a, b error) bool {
		return a == nil && b == nil || a != nil && b != nil && errors.Is(a, b) && errors.Is(b, a)
	}
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"matches naive and conserves bytes", func(t *testing.T) {
			s, n, r := drr.New(), NewNaive(), rand.New(rand.NewSource(1))
			for i := 0; i < 2000; i++ {
				id := r.Intn(10)
				switch r.Intn(3) {
				case 0:
					q := 1 + r.Intn(drr.MaxBlock)
					if !sameErr(s.AddFlow(id, q), n.AddFlow(id, q)) {
						t.Fatal("add mismatch")
					}
				case 1:
					size := r.Intn(drr.MaxBlock + 2)
					if !sameErr(s.Enqueue(id, size), n.Enqueue(id, size)) {
						t.Fatal("enqueue mismatch")
					}
				default:
					ai, as, ao := s.Dequeue()
					bi, bs, bo := n.Dequeue()
					if ai != bi || as != bs || ao != bo {
						t.Fatalf("dequeue mismatch: (%d,%d,%v) vs (%d,%d,%v)", ai, as, ao, bi, bs, bo)
					}
				}
			}
			for _, st := range s.Stats() {
				if st.SentBytes+st.QueuedBytes != st.EnqueuedBytes {
					t.Fatalf("flow %d is not conserved", st.ID)
				}
			}
		}},
		{"bounds equal-weight byte fairness", func(t *testing.T) {
			s := drr.New()
			must(t, s.AddFlow(1, drr.MaxBlock), s.AddFlow(2, drr.MaxBlock))
			r := rand.New(rand.NewSource(2))
			for i := 0; i < 2000; i++ {
				must(t, s.Enqueue(1, 1+r.Intn(drr.MaxBlock)), s.Enqueue(2, 1+r.Intn(drr.MaxBlock)))
			}
			sent := map[int]int{}
			for i := 0; i < 1000; i++ {
				id, size, ok := s.Dequeue()
				if !ok || sent[id]+size-sent[3-id] > 2*drr.MaxBlock || sent[3-id]-sent[id]-size > 2*drr.MaxBlock {
					t.Fatalf("fairness bound violated: id=%d size=%d sent=%v", id, size, sent)
				}
				sent[id] += size
			}
		}},
		{"resets deficit when flow becomes empty", func(t *testing.T) {
			q, s := drr.MaxBlock+1, drr.New()
			must(t, s.AddFlow(1, q), s.AddFlow(2, q))
			for i := 0; i < q-1; i++ {
				must(t, s.Enqueue(1, 1))
			}
			must(t, s.Enqueue(2, 1))
			for i := 0; i < q; i++ {
				if _, _, ok := s.Dequeue(); !ok {
					t.Fatal("initial drain failed")
				}
			}
			for i := 0; i < 1000; i++ {
				must(t, s.Enqueue(2, 1))
				if _, _, ok := s.Dequeue(); !ok {
					t.Fatal("idle-period drain failed")
				}
			}
			must(t, s.Enqueue(2, 1))
			for i := 0; i < 2*q-1; i++ {
				must(t, s.Enqueue(1, 1))
			}
			sent := 0
			for {
				id, size, ok := s.Dequeue()
				if !ok || id != 1 {
					break
				}
				sent += size
			}
			if sent > q+drr.MaxBlock-1 {
				t.Fatalf("correct burst %d exceeds bound", sent)
			}
			wrong := q - (q - 1)
			for credit := wrong + q; credit > 0 && wrong < 2*q-1; credit-- {
				wrong++
			}
			if wrong <= q+drr.MaxBlock-1 {
				t.Fatal("retained-deficit model did not expose the burst")
			}
		}},
		{"does not scan idle flows", func(t *testing.T) {
			s := drr.New()
			for i := 0; i < 10000; i++ {
				must(t, s.AddFlow(i, drr.MaxBlock))
			}
			must(t, s.AddFlow(10000, drr.MaxBlock), s.AddFlow(10001, drr.MaxBlock))
			must(t, s.Enqueue(10000, drr.MaxBlock), s.Enqueue(10001, drr.MaxBlock))
			if _, _, ok := s.Dequeue(); !ok || s.FlowChecks() > 3 {
				t.Fatalf("checked %d flows", s.FlowChecks())
			}
		}},
		{"returns side-effect-free sentinel errors", func(t *testing.T) {
			s := drr.New()
			must(t, s.AddFlow(1, drr.MaxBlock))
			calls := []func() error{
				func() error { return s.Enqueue(2, 1) },
				func() error { return s.Enqueue(1, 0) },
				func() error { return s.Enqueue(1, drr.MaxBlock+1) },
				func() error { return s.AddFlow(1, 1) },
				func() error { return s.AddFlow(2, 0) },
			}
			want := []error{drr.ErrUnknownFlow, drr.ErrBadSize, drr.ErrBadSize, drr.ErrExists, drr.ErrBadSize}
			for i, call := range calls {
				if err := call(); !errors.Is(err, want[i]) {
					t.Fatalf("call %d got %v", i, err)
				}
			}
			must(t, s.Enqueue(1, 1))
			if st := s.Stats(); st[0].QueuedBlocks != 1 || st[0].QueuedBytes != 1 {
				t.Fatal("failed calls changed state")
			}
		}},
		{"enforces MaxQueued without extra state change", func(t *testing.T) {
			s := drr.New()
			must(t, s.AddFlow(1, drr.MaxBlock))
			for i := 0; i < drr.MaxQueued; i++ {
				must(t, s.Enqueue(1, 1))
			}
			if !errors.Is(s.Enqueue(1, 1), drr.ErrFull) || s.Stats()[0].QueuedBlocks != drr.MaxQueued {
				t.Fatal("full queue behavior changed state")
			}
		}},
		{"supports concurrent enqueue and one dequeuer", func(t *testing.T) {
			const flows, per = 8, 200
			s := drr.New()
			for i := 0; i < flows; i++ {
				must(t, s.AddFlow(i, drr.MaxBlock))
			}
			var producers sync.WaitGroup
			for f := 0; f < flows; f++ {
				producers.Add(1)
				go func(id int) {
					defer producers.Done()
					for j := 0; j < per; j++ {
						must(t, s.Enqueue(id, 1+(id*31+j)%drr.MaxBlock))
					}
				}(f)
			}
			done := make(chan struct{})
			go func() {
				for got := 0; got < flows*per; {
					if _, _, ok := s.Dequeue(); ok {
						got++
					}
				}
				close(done)
			}()
			producers.Wait()
			<-done
			for _, st := range s.Stats() {
				want := 0
				for j := 0; j < per; j++ {
					want += 1 + (st.ID*31+j)%drr.MaxBlock
				}
				if st.EnqueuedBytes != want || st.SentBytes+st.QueuedBytes != want {
					t.Fatalf("flow %d is not conserved", st.ID)
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func must(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
