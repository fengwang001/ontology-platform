package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hash"
)

// runRandom 执行随机插入/删除序列，返回过滤器、应存在的键集与净指纹数。
func runRandom(t *testing.T, nb, epb, mk, ops, keys int, seed int64) (*api.Filter, map[int64]bool, int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	f, err := api.New(nb, epb, mk)
	if err != nil {
		t.Fatal(err)
	}
	present, net := map[int64]bool{}, 0
	for i := 0; i < ops; i++ {
		x := int64(rng.Intn(keys))
		if rng.Intn(2) == 0 {
			if f.Insert(x) == nil {
				present[x], net = true, net+1
			}
		} else if present[x] && f.Delete(x) == nil {
			present[x], net = false, net-1
		}
	}
	return f, present, net
}

func TestNoFalseNegatives(t *testing.T) {
	for _, cfg := range [][3]int{{16, 2, 4}, {64, 4, 8}, {256, 4, 16}} {
		f, present, _ := runRandom(t, cfg[0], cfg[1], cfg[2], 3000, 600, 42)
		for x, p := range present {
			if p && !f.Lookup(x) {
				t.Fatalf("cfg=%v 假阴性: %d", cfg, x)
			}
		}
	}
}

func TestMembershipConsistent(t *testing.T) {
	f, _, _ := runRandom(t, 64, 4, 8, 2000, 500, 7)
	bs := f.Buckets()
	for x := int64(0); x < 600; x++ {
		fp := hash.Fingerprint(x)
		want := slices.Contains(bs[hash.I1(x, 64)], fp) || slices.Contains(bs[hash.I2(x, 64)], fp)
		if f.Lookup(x) != want {
			t.Fatalf("x=%d Lookup 与两桶朴素扫描不一致", x)
		}
	}
}

func TestFingerprintConservation(t *testing.T) {
	f, _, net := runRandom(t, 64, 4, 8, 2000, 500, 99)
	total := 0
	for _, b := range f.Buckets() {
		total += len(slices.DeleteFunc(slices.Clone(b), func(v int) bool { return v == 0 }))
	}
	if total != net || f.Count() != net {
		t.Fatalf("指纹不守恒: 桶内=%d 计数=%d 净插入=%d", total, f.Count(), net)
	}
}

func TestConcurrentLookup(t *testing.T) {
	f, _ := api.New(256, 4, 8)
	for x := int64(0); x < 300; x++ {
		f.Insert(x)
	}
	const G, N = 8, 600
	want := make([]bool, N)
	for x := 0; x < N; x++ {
		want[x] = f.Lookup(int64(x))
	}
	got := make([][]bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]bool, N)
			for x := 0; x < N; x++ {
				got[g][x] = f.Lookup(int64(x))
			}
		}(g)
	}
	wg.Wait()
	for g := range got {
		if !reflect.DeepEqual(got[g], want) {
			t.Fatalf("goroutine %d 的 Lookup 结果不一致", g)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestSentinelErrors 四类哨兵错误经 api 包装后仍可判定且互不相同。
func TestSentinelErrors(t *testing.T) {
	_, e1 := api.New(3, 2, 1)
	f, _ := api.New(4, 1, 1)
	e2 := f.Insert(-1)
	e3 := f.Delete(7)
	for _, x := range []int64{1, 5, 2, 4} {
		f.Insert(x)
	}
	e4 := f.Insert(8)
	errs := []error{e1, e2, e3, e4}
	want := []error{api.ErrInvalidParams, api.ErrNegativeKey, api.ErrNotInserted, api.ErrFull}
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			t.Errorf("errs[%d]=%v, want %v", i, errs[i], want[i])
		}
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Errorf("错误 %v 与 %v 不可区分", errs[i], errs[j])
			}
		}
	}
	if !f.Lookup(1) || f.Lookup(-1) {
		t.Error("api 包装行为异常")
	}
}
