package endpointshard

import (
	"fmt"
	"math/rand"
	"sort"
	"sync/atomic"
	"testing"
)

func TestOrderedSetInsertRemoveIterate(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := newOrderedSet(lessString, rng)
	var ctr atomic.Uint64

	keys := []string{"delta", "alpha", "charlie", "bravo", "echo"}
	for _, k := range keys {
		if !s.Insert(k, &ctr) {
			t.Fatalf("insert %q: expected inserted=true", k)
		}
	}
	if s.Insert("alpha", &ctr) {
		t.Fatalf("duplicate insert: expected inserted=false")
	}
	if s.Len() != len(keys) {
		t.Fatalf("len = %d, want %d", s.Len(), len(keys))
	}

	var got []string
	s.Ascend(&ctr, func(k string) bool {
		got = append(got, k)
		return true
	})
	want := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ascend = %v, want %v", got, want)
	}

	if min, ok := s.Min(&ctr); !ok || min != "alpha" {
		t.Fatalf("min = %q,%v, want alpha,true", min, ok)
	}
	if two := s.FirstTwo(&ctr); fmt.Sprint(two) != "[alpha bravo]" {
		t.Fatalf("firstTwo = %v, want [alpha bravo]", two)
	}

	if s.Remove("missing", &ctr) {
		t.Fatalf("remove missing: expected removed=false")
	}
	if !s.Remove("charlie", &ctr) {
		t.Fatalf("remove charlie: expected removed=true")
	}
	got = got[:0]
	s.AscendFrom("bravo", &ctr, func(k string) bool {
		got = append(got, k)
		return true
	})
	if fmt.Sprint(got) != "[bravo delta echo]" {
		t.Fatalf("ascendFrom = %v, want [bravo delta echo]", got)
	}
	t.Logf("输入: 插入 %v 后删除 charlie; 实际输出: %v; 判定依据: 升序且不含已删键", keys, got)
}

func TestOrderedSetRandomizedAgainstSort(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := newOrderedSet(lessString, rng)
	var ctr atomic.Uint64
	ref := map[string]bool{}

	for i := 0; i < 3000; i++ {
		k := fmt.Sprintf("k%04d", rng.Intn(500))
		if rng.Intn(2) == 0 {
			s.Insert(k, &ctr)
			ref[k] = true
		} else {
			s.Remove(k, &ctr)
			delete(ref, k)
		}
	}
	var got []string
	s.Ascend(&ctr, func(k string) bool {
		got = append(got, k)
		return true
	})
	var want []string
	for k := range ref {
		want = append(want, k)
	}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("mismatch:\n got %v\nwant %v", got, want)
	}
	if s.Len() != len(want) {
		t.Fatalf("len = %d, want %d", s.Len(), len(want))
	}
}
