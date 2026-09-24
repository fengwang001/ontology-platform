package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"ontology/api"
)

type op struct{ k, ch, v int } // k: 0=Arrive 1=Step 2=Barrier(v=n)
type tcase struct {
	max  int
	ops  []op
	want error
}

func applyErr(o *api.Op, x op) error {
	switch x.k {
	case 0:
		return o.Arrive(x.ch, x.v)
	case 1:
		return o.Step(x.ch)
	}
	return o.Barrier(x.ch, x.v)
}
func sameView(a, b api.State) bool { return reflect.DeepEqual(a, b) }
func wantErr(t *testing.T, err, want error) {
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// 不变量 1（对齐一致）与 2（恰好一次）：记录值全为 1，cSum+CS 条数 == 屏障前到达条数。
func TestAlignedExactlyOnce(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		r := rand.New(rand.NewSource(seed))
		o := api.New(1 << 20)
		var tot, pre [2]int
		n, bar := 1, [2]bool{}
		for i := 0; i < 80; i++ {
			switch ch, sw := r.Intn(2), r.Intn(5); sw {
			case 0, 1, 2:
				o.Arrive(ch+1, 1)
				tot[ch]++
			case 3:
				o.Step(ch + 1)
			case 4:
				if !bar[ch] && o.Barrier(ch+1, n) == nil {
					bar[ch], pre[ch] = true, tot[ch]
				}
			}
			if bar[0] && bar[1] {
				s := o.Snapshot()
				for k, st := range s.State {
					if pre[k] != s.Sum[k]+len(st) {
						t.Fatalf("seed %d ch %d", seed, k+1)
					}
				}
				bar, n = [2]bool{}, n+1
			}
		}
	}
}

func TestRestoreEquivalence(t *testing.T) {
	for _, r := range api.SelfCheck() {
		if r.Err != nil {
			t.Fatalf("%s: %v", r.Name, r.Err)
		}
	}
}

// 不变量 4（失败不留痕）：四类错误互不相同、被拒后状态不变且可继续用。
func TestRejectNoMutation(t *testing.T) {
	if len(map[error]bool{api.ErrBadChannel: true, api.ErrEmptyQueue: true, api.ErrBarrierSeq: true, api.ErrBarrierDup: true, api.ErrStateFull: true}) != 5 {
		t.Fatal("sentinels not distinct")
	}
	cases := []tcase{
		{10, []op{{0, 3, 1}}, api.ErrBadChannel},
		{10, []op{{1, 1, 0}}, api.ErrEmptyQueue},
		{10, []op{{2, 1, 2}}, api.ErrBarrierSeq},
		{10, []op{{2, 1, 1}, {2, 1, 1}}, api.ErrBarrierDup},
		{2, []op{{0, 2, 1}, {2, 1, 1}, {0, 2, 2}, {0, 2, 3}}, api.ErrStateFull}, // Arrive 时超限
		{1, []op{{0, 2, 1}, {0, 2, 2}, {2, 1, 1}}, api.ErrStateFull},            // Barrier 时超限
	}
	for _, c := range cases {
		o := api.New(c.max)
		var err error
		for _, x := range c.ops {
			err = applyErr(o, x)
		}
		wantErr(t, err, c.want)
	}
	o := api.New(10)
	for _, x := range []op{{0, 2, 5}, {1, 2, 0}, {0, 2, 2}, {0, 1, 4}, {2, 1, 1}, {1, 2, 0}, {0, 2, 6}, {0, 1, 7}, {2, 2, 1}} {
		applyErr(o, x)
	}
	before := o.State()
	wantErr(t, o.Arrive(9, 1), api.ErrBadChannel)
	wantErr(t, o.Barrier(1, 3), api.ErrBarrierSeq)
	if !sameView(before, o.State()) {
		t.Fatal("rejected op mutated state")
	}
	if err := o.Arrive(1, 9); err != nil || o.Step(1) != nil {
		t.Fatal("operator not usable after reject")
	}
}

func TestConcurrent(t *testing.T) {
	const N = 300
	o := api.New(1 << 20)
	var wg sync.WaitGroup
	for ch := 1; ch <= 2; ch++ {
		wg.Add(2)
		go func(ch int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if i == N/2 {
					o.Barrier(ch, 1)
				}
				o.Arrive(ch, 1)
			}
		}(ch)
		go func(ch int) {
			defer wg.Done()
			for n := 0; n < N; {
				if o.Step(ch) == nil {
					n++
				} else {
					runtime.Gosched()
				}
			}
		}(ch)
	}
	wg.Wait()
	if s := o.Snapshot(); s.Sum[0]+len(s.State[0]) != N/2 || s.Sum[1]+len(s.State[1]) != N/2 {
		t.Fatalf("Sum=%v CS=%v/%v", s.Sum, s.State[0], s.State[1])
	}
	o.RunAll()
	direct := o.State()
	o.Restore()
	o.RunAll()
	if !sameView(direct, o.State()) {
		t.Fatal("restore mismatch after concurrent run")
	}
}
