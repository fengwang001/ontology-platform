package mapc

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/sch"
)

// 证明单列定位经 hash map O(1)：比较次数不随 active 列数 m 线性增长。
func TestLookupComparisonsConstant(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		lay := make([]sch.Column, m)
		vals := make([]any, m)
		for i := range lay {
			lay[i] = sch.Column{Name: fmt.Sprintf("c%d", i), Typ: sch.Int}
			vals[i] = int64(i)
		}
		mp := New()
		got, err := mp.Map(lay, lay, vals)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != m {
			t.Fatalf("m=%d 列数 %d", m, len(got))
		}
		if cmp := mp.lastCmp.Load(); cmp > 2 {
			t.Fatalf("m=%d 单列定位比较 %d 次，随 m 线性增长", m, cmp)
		}
	}
}

// 表驱动：对齐、补零、拒收、坏值、值数不符。
func TestMap(t *testing.T) {
	lay := []sch.Column{{Name: "a", Typ: sch.Int}, {Name: "b", Typ: sch.Str}}
	active := []sch.Column{
		{Name: "b", Typ: sch.Int},
		{Name: "c", Typ: sch.Str, Required: true},
		{Name: "d", Typ: sch.Int},
	}
	cases := []struct {
		vals []any
		err  error
	}{
		{[]any{int64(1), "2"}, sch.ErrMissingColumn},
		{[]any{int64(1)}, ErrArity},
		{[]any{int64(1), "xx"}, sch.ErrBadValue},
	}
	for i, c := range cases {
		if _, err := New().Map(lay, active, c.vals); err != c.err {
			t.Errorf("用例%d: %v ≠ %v", i, err, c.err)
		}
	}
	active[1].Required = false // c 改 optional 后补零值
	got, err := New().Map(lay, active, []any{int64(1), "2"})
	if err != nil || got[0] != int64(2) || got[1] != "" || got[2] != int64(0) {
		t.Errorf("补零: %v %v", got, err)
	}
}

// naiveMap 朴素参照：手工按列名对齐、逐列 coercion、缺失补零或拒收。
func naiveMap(active, lay []sch.Column, vals []any) ([]any, error) {
	out := make([]any, len(active))
	for i, ac := range active {
		found := false
		for j, c := range lay {
			if c.Name == ac.Name {
				v, err := sch.Coerce(vals[j], c.Typ, ac.Typ)
				if err != nil {
					return nil, err
				}
				out[i], found = v, true
			}
		}
		if !found {
			if ac.Required {
				return nil, sch.ErrMissingColumn
			}
			out[i] = zero(ac.Typ)
		}
	}
	return out, nil
}

// 不变量1：随机布局与随机事件下，Map 与朴素参照逐列相同。
func TestAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	names := []string{"a", "b", "c", "d", "e", "f"}
	for trial := 0; trial < 300; trial++ {
		mk := func() []sch.Column {
			var cols []sch.Column
			for _, n := range names {
				if rng.Intn(2) == 1 {
					cols = append(cols, sch.Column{Name: n, Typ: sch.Type(rng.Intn(2)), Required: rng.Intn(2) == 0})
				}
			}
			return cols
		}
		lay, active := mk(), mk()
		vals := make([]any, len(lay))
		for j, c := range lay {
			if c.Typ == sch.Int {
				vals[j] = int64(rng.Intn(100))
			} else if rng.Intn(3) == 0 {
				vals[j] = "abc"
			} else {
				vals[j] = fmt.Sprint(rng.Intn(100))
			}
		}
		want, werr := naiveMap(active, lay, vals)
		got, gerr := New().Map(lay, active, vals)
		if !errors.Is(gerr, werr) || (gerr == nil && !reflect.DeepEqual(got, want)) {
			t.Fatalf("trial %d: got %v,%v want %v,%v", trial, got, gerr, want, werr)
		}
	}
}
