package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/internal/api"
	"ontology/internal/route"
)

// naive 把全部 key 按当前 n 重新哈希分桶（不变量 2 的参照系）。
func naive(kv map[string]string, n int) []map[string]string {
	out := make([]map[string]string, n)
	for i := range out {
		out[i] = map[string]string{}
	}
	for k, v := range kv {
		out[route.Home(k, n)][k] = v
	}
	return out
}

// setupAE：New(2)，Put a..e（大写值），Rebalance(3)。
func setupAE() *api.Store {
	st, _ := api.New(2)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = st.Put(k, string(k[0]-32))
	}
	_ = st.Rebalance(3)
	return st
}

// TestRebalanceMatchesNaive 钉住不变量 2：任意 Put/Rebalance 后等于朴素重哈希。
func TestRebalanceMatchesNaive(t *testing.T) {
	cases := []struct {
		n    int
		puts [][2]string
		news []int
	}{
		{2, [][2]string{{"a", "A"}, {"b", "B"}, {"c", "C"}, {"d", "D"}, {"e", "E"}}, []int{3}},
		{7, [][2]string{{"a", "A"}, {"bb", "BB"}, {"xyz", "XYZ"}, {"q", "Q"}}, []int{2}},
		{3, [][2]string{{"a", "A"}, {"a", "A2"}, {"bb", "BB"}}, []int{1, 6, 2, 9, 4}},
	}
	for ti, tc := range cases {
		st, _ := api.New(tc.n)
		kv := map[string]string{}
		for _, p := range tc.puts {
			_ = st.Put(p[0], p[1])
			kv[p[0]] = p[1]
		}
		if !reflect.DeepEqual(st.Dump(), naive(kv, tc.n)) {
			t.Fatalf("case %d mismatch after puts", ti)
		}
		for _, n := range tc.news {
			if err := st.Rebalance(n); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(st.Dump(), naive(kv, n)) {
				t.Fatalf("case %d mismatch after Rebalance(%d)", ti, n)
			}
		}
	}
}

// TestGetPartitionMoved 钉住不变量 3：p!=home 恒 moved(to home)，p==home 报真实存在性。
func TestGetPartitionMoved(t *testing.T) {
	st := setupAE()
	cases := []struct {
		p    int
		key  string
		want api.PartitionResult
	}{
		{0, "b", api.PartitionResult{Moved: true, MovedTo: 2}},
		{2, "b", api.PartitionResult{Found: true, Value: "B"}},
		{0, "c", api.PartitionResult{Found: true, Value: "C"}},
		{1, "y", api.PartitionResult{}}, // 121%3=1 且 y 未存 → 真实 not found
	}
	for _, tc := range cases {
		got, err := st.GetPartition(tc.p, tc.key)
		if err != nil || got != tc.want {
			t.Fatalf("p=%d k=%s got=%+v err=%v", tc.p, tc.key, got, err)
		}
	}
}

// TestRejectedOpsNoTrace 钉住不变量 4：三类哨兵互异、拒绝不留痕、拒绝后仍可用。
func TestRejectedOpsNoTrace(t *testing.T) {
	st, _ := api.New(2)
	_ = st.Put("a", "A")
	_ = st.Rebalance(3)
	before := st.Dump()
	calls := []struct {
		want error
		f    func() error
	}{
		{api.ErrInvalidN, func() error { _, e := api.New(0); return e }},
		{api.ErrInvalidN, func() error { return st.Rebalance(0) }},
		{api.ErrPartitionOutOfRange, func() error { _, e := st.GetPartition(3, "a"); return e }},
		{api.ErrPartitionOutOfRange, func() error { _, e := st.GetPartition(-1, "a"); return e }},
		{api.ErrEmptyKey, func() error { return st.Put("", "X") }},
		{api.ErrEmptyKey, func() error { _, _, e := st.Get(""); return e }},
		{api.ErrEmptyKey, func() error { _, e := st.GetPartition(0, ""); return e }},
	}
	for _, c := range calls {
		if !errors.Is(c.f(), c.want) {
			t.Fatalf("want %v", c.want)
		}
	}
	if api.ErrInvalidN == api.ErrPartitionOutOfRange || api.ErrInvalidN == api.ErrEmptyKey || api.ErrPartitionOutOfRange == api.ErrEmptyKey {
		t.Fatal("sentinels must be pairwise distinct")
	}
	if !reflect.DeepEqual(before, st.Dump()) {
		t.Fatal("state changed despite rejected ops")
	}
	if err := st.Put("z", "Z"); err != nil || st.SelfCheck() != nil {
		t.Fatal("store unusable or SelfCheck failed after rejections")
	}
}

// TestConcurrentReaders 钉住并发只读：每次只读压成签名，结果须一致；关 start 同步，无 sleep。
func TestConcurrentReaders(t *testing.T) {
	st := setupAE()
	const g = 32
	sigs := make([]string, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(g)
	for i := 0; i < g; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			v, ok, _ := st.Get("b")
			r, _ := st.GetPartition(0, "b")
			sigs[i] = fmt.Sprintf("%q %v %+v %v", v, ok, r, st.Dump())
			if err := st.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < g; i++ {
		if sigs[i] != sigs[0] {
			t.Fatalf("goroutine %d result differs", i)
		}
	}
}
