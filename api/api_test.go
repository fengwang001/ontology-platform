package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	e, _ := New(100)
	before := state(e)
	if err := e.SelfCheck(); err != nil || state(e) != before {
		t.Fatalf("SelfCheck err=%v or mutated receiver (%s -> %s)", err, before, state(e))
	}
}
func TestJoinMatchesBatchRecompute(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		r := rand.New(rand.NewSource(seed))
		capb := int64(20 + r.Intn(120))
		e, _ := New(capb)
		ref := []map[string]string{{}} // v0 empty; oracle is never evicted
		for i, nBc := 0, 1+r.Intn(8); i < nBc; i++ {
			batch := []Entry{}
			for j, n := 0, 1+r.Intn(3); j < n; j++ {
				batch = append(batch, Entry{Key: string(rune('a' + r.Intn(6))), Val: fmt.Sprint(r.Intn(1000))})
			}
			if _, err := e.Broadcast(batch); err != nil {
				continue // legal oversized-batch reject: engine and ref both stay put
			}
			cur := map[string]string{}
			for k, v := range ref[len(ref)-1] {
				cur[k] = v
			}
			for _, en := range batch {
				cur[en.Key] = en.Val
			}
			ref = append(ref, cur)
		}
		oldest := e.st.Versions()[0]
		var wRows []Row
		var wDrop, wMiss int64
		for i, n := 0, 50+r.Intn(100); i < n; i++ {
			f := Fact{Key: string(rune('a' + r.Intn(8))), Vsn: r.Int63n(e.st.V() + 1)}
			got, err := e.Join(f) // streaming result
			if err != nil {
				t.Fatalf("seed %d join: %v", seed, err)
			}
			switch {
			case f.Vsn < oldest: // evicted -> stale drop, no row
				wDrop++
				if got != (Row{}) {
					t.Fatalf("seed %d stale produced %+v", seed, got)
				}
			default: // batch recompute against the snapshot f.Vsn names
				v, ok := ref[f.Vsn][f.Key]
				if !ok {
					wMiss++
					if got != (Row{}) {
						t.Fatalf("seed %d miss produced %+v", seed, got)
					}
				} else {
					w := Row{Key: f.Key, Val: v, Vsn: f.Vsn}
					wRows = append(wRows, w)
					if got != w {
						t.Fatalf("seed %d got %+v want %+v", seed, got, w)
					}
				}
			}
		}
		if fmt.Sprint(e.Joined()) != fmt.Sprint(wRows) || e.Dropped() != wDrop ||
			e.Missed() != wMiss || e.st.Used() > capb {
			t.Fatalf("seed %d mismatch rows=%v/%v d=%d/%d m=%d/%d used=%d cap=%d",
				seed, e.Joined(), wRows, e.Dropped(), wDrop, e.Missed(), wMiss, e.st.Used(), capb)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrMaxBytes) {
		t.Fatalf("New(0)=%v want ErrMaxBytes", err)
	}
	e, _ := New(100)
	e.Broadcast([]Entry{{Key: "a", Val: "1"}})
	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"empty batch", ErrEmptyBatch, func() error { _, er := e.Broadcast(nil); return er }},
		{"empty entry key", ErrEmptyKey, func() error { _, er := e.Broadcast([]Entry{{Key: "", Val: "z"}}); return er }},
		{"empty fact key", ErrEmptyKey, func() error { _, er := e.Join(Fact{}); return er }},
		{"future vsn", ErrFuture, func() error { _, er := e.Join(Fact{Key: "a", Vsn: e.st.V() + 1}); return er }},
	}
	for _, c := range cases {
		before := state(e)
		if err := c.call(); !errors.Is(err, c.want) || state(e) != before {
			t.Fatalf("%s: err or state changed (%s -> %s)", c.name, before, state(e))
		}
	}
	if _, err := e.Broadcast([]Entry{{Key: "b", Val: "2"}}); err != nil {
		t.Fatalf("usable after rejects: %v", err)
	}
	if r, err := e.Join(Fact{Key: "a", Vsn: 1}); err != nil || r.Val != "1" {
		t.Fatalf("post-reject join %+v %v", r, err)
	}
}
func TestConcurrentJoin(t *testing.T) {
	e, _ := New(1 << 20)
	var batch []Entry
	for i := 0; i < 200; i++ {
		batch = append(batch, Entry{Key: fmt.Sprintf("k%03d", i), Val: fmt.Sprint(i)})
	}
	if _, err := e.Broadcast(batch); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	errc := make(chan error, n*3)
	baseView := fmt.Sprint(e.View())
	for g := 0; g < n; g++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			w := Row{Key: "k100", Val: "100", Vsn: 1}
			if r, err := e.Join(Fact{Key: "k100", Vsn: 1}); err != nil || r != w {
				errc <- fmt.Errorf("same: %+v %v", r, err)
			}
		}()
		go func(g int) {
			defer wg.Done()
			k := fmt.Sprintf("k%03d", g%200)
			w := Row{Key: k, Val: fmt.Sprint(g % 200), Vsn: 1}
			if r, err := e.Join(Fact{Key: k, Vsn: 1}); err != nil || r != w {
				errc <- fmt.Errorf("diff: %+v %v", r, err)
			}
		}(g)
		go func() {
			defer wg.Done()
			if err := e.SelfCheck(); err != nil || fmt.Sprint(e.View()) != baseView {
				errc <- fmt.Errorf("selfcheck/view: %v", err)
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
}
