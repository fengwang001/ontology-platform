package api_test

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/delta"
	"ontology/replica"
)

func k(i int) string { return string(rune('a' + i%26)) }

func set(key string, v int) delta.Change { return delta.Change{Kind: delta.Set, Key: key, Val: v} }
func del(key string) delta.Change        { return delta.Change{Kind: delta.Del, Key: key} }

// naiveReplay 是不变量 1 的参照：从版本 0 起，From 等于当前版本才应用。
func naiveReplay(ds []delta.Delta) (int, map[string]int) {
	v := 0
	st := map[string]int{}
	for _, d := range ds {
		if d.From != v {
			continue
		}
		for _, c := range d.Changes {
			delta.ApplyChange(st, c)
		}
		v = d.To
	}
	return v, st
}

func TestMatchesNaiveReplay(t *testing.T) {
	for _, tc := range [][3]int{{1, 10, 20}, {2, 100, 200}, {3, 1000, 500}} { // seed, n, extra
		rng := rand.New(rand.NewSource(int64(tc[0])))
		var ds []delta.Delta
		for i := 0; i < tc[1]; i++ { // 先按序喂 n 个合法 delta
			ds = append(ds, delta.Delta{From: i, To: i + 1,
				Changes: []delta.Change{set(k(i), i), del(k(i + 1))}})
		}
		for i := 0; i < tc[2]; i++ { // 再随机混入重复与乱序
			f := rng.Intn(tc[1] + 5)
			ds = append(ds, delta.Delta{From: f, To: f + 1, Changes: []delta.Change{set("z", f)}})
		}
		a := api.New()
		for _, d := range ds {
			_ = a.Apply(d)
		}
		wantV, wantS := naiveReplay(ds)
		if a.Version() != wantV || !reflect.DeepEqual(a.State(), wantS) {
			t.Fatalf("seed=%d: got v=%d s=%v, want v=%d s=%v", tc[0], a.Version(), a.State(), wantV, wantS)
		}
	}
}

func TestVersionMonotonic(t *testing.T) {
	a := api.New()
	prev := a.Version()
	for i := 0; i < 200; i++ {
		d := delta.Delta{From: i, To: i + 1}
		if err := a.Apply(d); err != nil {
			t.Fatal(err)
		}
		if a.Version() != d.To || a.Version() < prev {
			t.Fatalf("version not monotonic: prev=%d now=%d", prev, a.Version())
		}
		prev = a.Version()
	}
	_ = a.Apply(delta.Delta{From: 999, To: 1000}) // gap 被拒，版本不动
	if a.Version() != prev {
		t.Fatal("rejected delta moved version")
	}
}

func TestDuplicateIdempotent(t *testing.T) {
	a := api.New()
	ds := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{set("a", 1)}},
		{From: 1, To: 2, Changes: []delta.Change{set("b", 2)}},
	}
	for _, d := range ds {
		if err := a.Apply(d); err != nil {
			t.Fatal(err)
		}
	}
	wantS, wantV := a.State(), a.Version()
	for round := 0; round < 3; round++ { // 多轮重复喂入旧 delta
		for _, d := range ds {
			if err := a.Apply(d); err != nil {
				t.Fatal(err)
			}
		}
		if a.Version() != wantV || !reflect.DeepEqual(a.State(), wantS) {
			t.Fatal("duplicate delta changed state or version")
		}
	}
}

func TestRejectedKeepsState(t *testing.T) {
	names := []string{"gap", "to<=from", "neg from", "neg to", "empty key"}
	bads := []delta.Delta{
		{From: 5, To: 6}, {From: 3, To: 3}, {From: -1, To: 1}, {From: 0, To: -1},
		{From: 0, To: 1, Changes: []delta.Change{set("", 1)}},
	}
	wants := []error{replica.ErrGap, replica.ErrInvalidRange,
		replica.ErrNegativeVersion, replica.ErrNegativeVersion, replica.ErrEmptyKey}
	for i, want := range wants {
		a := api.New()
		_ = a.Apply(delta.Delta{From: 0, To: 1, Changes: []delta.Change{set("k", 9)}})
		beforeS, beforeV := a.State(), a.Version()
		if err := a.Apply(bads[i]); err != want {
			t.Fatalf("%s: got err %v, want %v", names[i], err, want)
		}
		if a.Version() != beforeV || !reflect.DeepEqual(a.State(), beforeS) {
			t.Fatalf("%s: rejected delta left trace", names[i])
		}
		if err := a.Apply(delta.Delta{From: 1, To: 2}); err != nil { // 被拒后仍可用
			t.Fatalf("%s: instance unusable after rejection: %v", names[i], err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
