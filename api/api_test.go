package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func snap(a *api.API) string {
	return fmt.Sprintf("%d|%d|%d|%v", a.CP(), a.Marker(), a.First(), a.Read(0))
}

func prep4(a *api.API) {
	for i := 0; i < 4; i++ {
		a.Append("x")
	}
}

func coherent(a *api.API, ref map[uint64]string) bool {
	es := a.Read(0)
	if uint64(len(es)) != uint64(len(ref))-a.First() {
		return false
	}
	for i, e := range es {
		if e.Offset != a.First()+uint64(i) || ref[e.Offset] != e.Payload {
			return false
		}
	}
	return true
}

func TestReferenceEquivalence(t *testing.T) {
	for _, seed := range []int64{1, 2, 42, 100} {
		r, a, ref := rand.New(rand.NewSource(seed)), api.New(), map[uint64]string{}
		for step := 0; step < 800; step++ {
			switch r.Intn(4) {
			case 0, 1:
				off, _ := a.Append(fmt.Sprintf("p%d", step))
				ref[off] = fmt.Sprintf("p%d", step)
			case 2:
				if n := uint64(len(ref)); n > 0 && int64(n-1) > a.CP() && r.Intn(2) == 0 {
					lo := uint64(a.CP() + 1)
					a.Checkpoint(lo + uint64(r.Intn(int(n-lo))))
				}
			case 3: // 随机截断，或随机“标记后删除前崩溃”再双判定恢复
				if cp := a.CP(); cp >= 0 && uint64(cp) > a.First() {
					k := a.First() + uint64(r.Intn(int(uint64(cp)-a.First()+1)))
					if r.Intn(2) == 0 {
						a.Truncate(k)
					} else if a.Recover(k, a.First()) != nil || a.Marker() != k || a.First() != k {
						t.Fatalf("seed=%d: interrupted recovery did not converge", seed)
					}
				}
			}
			if !coherent(a, ref) {
				t.Fatalf("seed=%d step=%d: drift from naive reference", seed, step)
			}
		}
	}
}

func TestFailuresLeaveNoTrace(t *testing.T) {
	cases := []struct {
		prep int
		want error
		fn   func(*api.API) error
	}{
		{0, api.ErrCheckpointInvalid, func(a *api.API) error { return a.Checkpoint(0) }},
		{1, api.ErrCheckpointInvalid, func(a *api.API) error { return a.Checkpoint(4) }},
		{2, api.ErrCheckpointInvalid, func(a *api.API) error { return a.Checkpoint(1) }},
		{1, api.ErrTruncateBeyondCP, func(a *api.API) error { return a.Truncate(1) }},
		{2, api.ErrTruncateBeyondCP, func(a *api.API) error { return a.Truncate(3) }},
		{3, api.ErrRecoverOverDeleted, func(a *api.API) error { return a.Recover(2, 3) }},
	}
	for i, c := range cases {
		a := api.New()
		if c.prep >= 1 {
			prep4(a)
		}
		if c.prep >= 2 {
			a.Checkpoint(2)
		}
		if c.prep >= 3 {
			a.Truncate(2)
		}
		before := snap(a)
		if err := c.fn(a); !errors.Is(err, c.want) || before != snap(a) {
			t.Fatalf("case %d: err=%v or rejected op left a trace", i, err)
		}
	}
	if api.ErrCheckpointInvalid == api.ErrTruncateBeyondCP || api.ErrCheckpointInvalid == api.ErrRecoverOverDeleted || api.ErrTruncateBeyondCP == api.ErrRecoverOverDeleted {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	a := api.New() // 被 Truncate 拒绝后同实例仍可继续正常使用
	prep4(a)
	if a.Checkpoint(2) != nil || !errors.Is(a.Truncate(3), api.ErrTruncateBeyondCP) || a.Truncate(2) != nil || a.First() != 2 {
		t.Fatal("system unusable after rejections")
	}
}

func TestConcurrentCoherent(t *testing.T) {
	a := api.New()
	var bad int32
	var wg sync.WaitGroup
	wg.Add(9)
	for r := 0; r < 8; r++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if id == 0 && j%10 == 0 && a.SelfCheck() != nil {
					atomic.StoreInt32(&bad, 1)
				}
				es := a.Read(uint64((j + id) % 64))
				for k := 1; k < len(es); k++ {
					if es[k].Offset != es[k-1].Offset+1 || es[k].Payload != fmt.Sprintf("p%d", es[k].Offset) {
						atomic.StoreInt32(&bad, 1)
					}
				}
			}
		}(r)
	}
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			a.Append(fmt.Sprintf("p%d", i))
			if i%8 == 7 {
				a.Checkpoint(uint64(i - 1))
				a.Truncate(uint64(i - 1))
			}
		}
	}()
	wg.Wait()
	if atomic.LoadInt32(&bad) != 0 {
		t.Fatal("reader observed incoherent partially-truncated interval")
	}
}
func TestSelfCheck(t *testing.T) {
	a := api.New()
	a.Append("z")
	before := snap(a)
	if a.SelfCheck() != nil || a.SelfCheck() != nil || before != snap(a) {
		t.Fatal("SelfCheck failed or mutated the receiver")
	}
}
