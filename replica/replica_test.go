package replica

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/delta"
)

// TestDedupReadCountConstant 证明去重/乱序判定是 O(1)：
// 连续应用 m 个 delta 后再喂一个 From==m 的新 delta（以及一个旧重复 delta），
// 为判定而读取的「已应用版本记录」个数不随 m 增长。
func TestDedupReadCountConstant(t *testing.T) {
	const maxReads = 1 // 只与单个版本号比较，与 m 无关的小常数
	for _, m := range []int{100, 1000, 5000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			if err := r.Apply(delta.Delta{From: i, To: i + 1}); err != nil {
				t.Fatalf("m=%d: apply %d: %v", m, i, err)
			}
		}
		if err := r.Apply(delta.Delta{From: m, To: m + 1}); err != nil {
			t.Fatalf("m=%d: apply in-order delta: %v", m, err)
		}
		if r.lastReads > maxReads {
			t.Fatalf("m=%d: in-order check read %d version records, want <= %d",
				m, r.lastReads, maxReads)
		}
		if err := r.Apply(delta.Delta{From: 0, To: 1}); err != nil { // 重复旧 delta
			t.Fatalf("m=%d: duplicate apply: %v", m, err)
		}
		if r.lastReads > maxReads {
			t.Fatalf("m=%d: dedup check read %d version records, want <= %d",
				m, r.lastReads, maxReads)
		}
		if err := r.Apply(delta.Delta{From: m + 5, To: m + 6}); err != ErrGap { // 乱序
			t.Fatalf("m=%d: gap: got %v", m, err)
		}
		if r.lastReads > maxReads {
			t.Fatalf("m=%d: gap check read %d version records, want <= %d",
				m, r.lastReads, maxReads)
		}
	}
}

// TestApplySemantics 表驱动钉住 replica 的核心判定语义。
func TestApplySemantics(t *testing.T) {
	cases := []struct {
		name    string
		ds      []delta.Delta
		wantV   int
		wantS   map[string]int
		wantErr []error // 每个 delta 对应的期望错误，nil 表示成功
	}{
		{"顺序应用与 Del 生效", []delta.Delta{
			{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 1}}},
			{From: 1, To: 2, Changes: []delta.Change{
				{Kind: delta.Set, Key: "b", Val: 2}, {Kind: delta.Del, Key: "a"}}},
		}, 2, map[string]int{"b": 2}, []error{nil, nil}},
		{"重复幂等", []delta.Delta{
			{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 1}}},
			{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 100}}},
		}, 1, map[string]int{"a": 1}, []error{nil, nil}},
		{"乱序拒绝", []delta.Delta{
			{From: 3, To: 4, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 1}}},
		}, 0, map[string]int{}, []error{ErrGap}},
		{"同 delta 内 Change 按序", []delta.Delta{
			{From: 0, To: 1, Changes: []delta.Change{
				{Kind: delta.Set, Key: "a", Val: 1},
				{Kind: delta.Set, Key: "a", Val: 2},
				{Kind: delta.Del, Key: "a"},
				{Kind: delta.Set, Key: "a", Val: 3}}},
		}, 1, map[string]int{"a": 3}, []error{nil}},
		{"Del 不存在的键无操作", []delta.Delta{
			{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Del, Key: "ghost"}}},
		}, 1, map[string]int{}, []error{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			for i, d := range tc.ds {
				if err := r.Apply(d); err != tc.wantErr[i] {
					t.Fatalf("delta %d: got err %v, want %v", i, err, tc.wantErr[i])
				}
			}
			if r.Version() != tc.wantV {
				t.Fatalf("version: got %d, want %d", r.Version(), tc.wantV)
			}
			got := r.State()
			if len(got) != len(tc.wantS) {
				t.Fatalf("state: got %v, want %v", got, tc.wantS)
			}
			for k, v := range tc.wantS {
				if got[k] != v {
					t.Fatalf("state[%q]: got %d, want %d", k, got[k], v)
				}
			}
		})
	}
}

// TestConcurrentReads 对同一已就绪副本并发只读，所有 goroutine 读到的
// (Version, State) 必须逐字段相同；用起跑栅栏而非 sleep 制造并发。
func TestConcurrentReads(t *testing.T) {
	r := New()
	for i := 0; i < 50; i++ {
		_ = r.Apply(delta.Delta{From: i, To: i + 1, Changes: []delta.Change{
			{Kind: delta.Set, Key: string(rune('a' + i%26)), Val: i}}})
	}
	wantV, wantS := r.Version(), r.State()
	start := make(chan struct{})
	var bad atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if r.Version() != wantV || !reflect.DeepEqual(r.State(), wantS) {
					bad.Add(1)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("inconsistent concurrent read")
	}
}
