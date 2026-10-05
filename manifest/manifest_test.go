package manifest

import (
	"math/rand"
	"reflect"
	"testing"
)

// 白盒验证：前沿只进不退，advance 累计不超过 档数 + 被接受的 Finish 数；
// L 与"每次从最低档重扫"的朴素结果一致；每版是前一版真超集。
func TestAdvanceCounterAndFrontier(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 1 + r.Intn(8)
		entries := make([]Entry, n)
		hasReq := false
		for i := range entries {
			req := r.Intn(2) == 0
			if i == n-1 && !hasReq {
				req = true
			}
			hasReq = hasRequired(hasReq, req)
			entries[i] = Entry{
				Name:     string(rune('a' + i)),
				Height:   (i + 1) * 100,
				Bitrate:  (i + 1) * 500,
				Required: req,
			}
		}
		m := New(entries)
		// 随机顺序让各档到达终态。
		order := r.Perm(n)
		finishes := 0
		var prev []Entry
		for _, idx := range order {
			done := r.Intn(4) != 0 // 75% Done
			size := int64(r.Intn(1000))
			m.OnTerminal(idx, done, size)
			if done {
				entries[idx].Size = size
			}
			finishes++
			if m.advance > n+finishes {
				t.Fatalf("seed=%d: advance=%d > 档数%d + Finish数%d", seed, m.advance, n, finishes)
			}
			// 与朴素重扫对照 L。
			wantL := naiveL(entries, m.state)
			gotL := make([]Entry, 0, len(m.listed))
			for _, ix := range m.listed {
				gotL = append(gotL, m.entries[ix])
			}
			if len(gotL) != len(wantL) || !reflect.DeepEqual(gotL, wantL) && len(wantL) > 0 {
				t.Fatalf("seed=%d: L=%v, 朴素重扫=%v", seed, gotL, wantL)
			}
			if v, ok := m.MaybePublish(); ok {
				if v.Number != len(m.versions) {
					t.Fatalf("seed=%d: 版本号不连续", seed)
				}
				if !strictSuperset(prev, v.Rungs) {
					t.Fatalf("seed=%d: 版本 %d 不是前一版真超集", seed, v.Number)
				}
				prev = v.Rungs
			}
		}
	}
}

func hasRequired(acc, req bool) bool { return acc || req }

// naiveL 朴素模拟：每次从最低档重扫，Failed 跳过，遇未决档即停。
func naiveL(entries []Entry, state []int8) []Entry {
	var out []Entry
	for i := range entries {
		switch state[i] {
		case stDone:
			out = append(out, entries[i])
		case stFailed:
		default:
			return out
		}
	}
	return out
}

func strictSuperset(prev, cur []Entry) bool {
	if len(cur) <= len(prev) {
		return false
	}
	set := make(map[Entry]bool, len(cur))
	for _, e := range cur {
		set[e] = true
	}
	for _, e := range prev {
		if !set[e] {
			return false
		}
	}
	return true
}

// 确定性小例：前沿不重扫，advance 恰好等于 档数 + 停滞的 Finish 数。
func TestAdvanceExactCount(t *testing.T) {
	entries := []Entry{
		{Name: "a", Height: 360, Bitrate: 800, Required: true},
		{Name: "b", Height: 720, Bitrate: 2500},
	}
	m := New(entries)
	m.OnTerminal(1, true, 10) // b 先 Done，前沿在 a 停滞：检查 1 次
	if m.advance != 1 {
		t.Fatalf("advance = %d, want 1", m.advance)
	}
	m.OnTerminal(0, true, 5) // a Done，前沿连过 a、b：检查 2 次
	if m.advance != 3 {
		t.Fatalf("advance = %d, want 3", m.advance)
	}
	v, ok := m.MaybePublish()
	if !ok || v.Number != 1 || len(v.Rungs) != 2 {
		t.Fatalf("publish = %+v, %v", v, ok)
	}
	if _, err := m.Latest(); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if _, err := m.Get(2); err != ErrVersionNotFound {
		t.Fatalf("Get(2) err = %v", err)
	}
}
