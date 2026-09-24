package merge3

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/conflict"
	"ontology/delta"
	"ontology/doc"
)

func rec(pairs ...any) doc.Record {
	r := doc.Record{}
	for i := 0; i < len(pairs); i += 2 {
		r[pairs[i].(string)] = pairs[i+1].(doc.Value)
	}
	return r
}

func setEqual(a, b doc.Set) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ra := range a {
		rb, ok := b[k]
		if !ok || !doc.RecordEqual(ra, rb) {
			return false
		}
	}
	return true
}

func kinds(rep conflict.Report) []conflict.Kind {
	out := []conflict.Kind{}
	for _, c := range rep.List {
		out = append(out, c.Kind)
	}
	return out
}

func TestCombinations(t *testing.T) {
	S, N := doc.Str, doc.Num
	cases := []struct {
		name      string
		anc, l, r doc.Set
		want      doc.Set
		wantKinds []conflict.Kind
	}{
		{"左改右未改", doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", N(2))}, doc.Set{"k": rec("f", N(1))},
			doc.Set{"k": rec("f", N(2))}, nil},
		{"两侧同值", doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", N(2))}, doc.Set{"k": rec("f", N(2))},
			doc.Set{"k": rec("f", N(2))}, nil},
		{"两侧异值", doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", N(2))}, doc.Set{"k": rec("f", N(3))},
			doc.Set{"k": rec("f", N(2))}, []conflict.Kind{conflict.FieldValue}},
		{"左删右改", doc.Set{"k": rec("f", N(1))}, doc.Set{}, doc.Set{"k": rec("f", N(2))},
			doc.Set{}, []conflict.Kind{conflict.DeleteVsModify}},
		{"两侧同删", doc.Set{"k": rec("f", N(1))}, doc.Set{}, doc.Set{},
			doc.Set{}, nil},
		{"改不同字段", doc.Set{"k": rec("a", N(1), "b", N(1), "c", N(1))},
			doc.Set{"k": rec("a", N(2), "b", N(1), "c", N(1))},
			doc.Set{"k": rec("a", N(1), "b", N(3), "c", N(1))},
			doc.Set{"k": rec("a", N(2), "b", N(3), "c", N(1))}, nil},
		{"新增同内容", doc.Set{}, doc.Set{"k": rec("f", S("x"))}, doc.Set{"k": rec("f", S("x"))},
			doc.Set{"k": rec("f", S("x"))}, nil},
		{"新增异内容", doc.Set{}, doc.Set{"k": rec("f", S("x"))}, doc.Set{"k": rec("f", S("y"))},
			doc.Set{"k": rec("f", S("x"))}, []conflict.Kind{conflict.AddAdd}},
		{"祖先缺失", doc.Set{"other": rec()}, doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", N(1))},
			doc.Set{"k": rec("f", N(1))}, nil},
		{"类型不一致", doc.Set{"k": rec("f", N(1))}, doc.Set{"k": rec("f", S("2"))}, doc.Set{"k": rec("f", N(2))},
			doc.Set{"k": rec("f", S("2"))}, []conflict.Kind{conflict.TypeMismatch}},
		{"三集合皆空", doc.Set{}, doc.Set{}, doc.Set{}, doc.Set{}, nil},
		{"祖先为空两侧有内容", doc.Set{}, doc.Set{"a": rec("f", N(1))}, doc.Set{"b": rec("f", N(2))},
			doc.Set{"a": rec("f", N(1)), "b": rec("f", N(2))}, nil},
		{"左侧为空即删全部", doc.Set{"k": rec("f", N(1))}, doc.Set{}, doc.Set{"k": rec("f", N(1))},
			doc.Set{}, nil},
		{"空串键与字段名", doc.Set{"": rec("", N(1))}, doc.Set{"": rec("", N(2))}, doc.Set{"": rec("", N(1))},
			doc.Set{"": rec("", N(2))}, nil},
		{"空串值区别于不存在", doc.Set{"k": rec()}, doc.Set{"k": rec("f", S(""))}, doc.Set{"k": rec()},
			doc.Set{"k": rec("f", S(""))}, nil},
	}
	for _, c := range cases {
		got, rep, _ := Merge(c.anc, c.l, c.r)
		if !setEqual(got, c.want) {
			t.Errorf("%s: 结果=%v 期望=%v", c.name, got, c.want)
		}
		gk := kinds(rep)
		if fmt.Sprint(gk) != fmt.Sprint(c.wantKinds) {
			t.Errorf("%s: 冲突=%v 期望=%v", c.name, gk, c.wantKinds)
		}
	}
}

func TestDeterministic(t *testing.T) {
	mk := func(rng *rand.Rand) doc.Set {
		s := doc.Set{}
		keys := []string{"a", "b", "c", "d", "e"}
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
		for _, k := range keys {
			s[k] = rec("x", doc.Num(1), "y", doc.Str("v"))
		}
		return s
	}
	var ref []byte
	for i := 0; i < 20; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		got, rep, _ := Merge(mk(rng), mk(rng), mk(rng))
		b := append(doc.Canonical(got), []byte(rep.String())...)
		if i == 0 {
			ref = b
		} else if string(b) != string(ref) {
			t.Fatalf("第 %d 次打乱后输出不同", i)
		}
	}
}

func TestLookupBound(t *testing.T) {
	const n = 50000
	mk := func() doc.Set {
		s := make(doc.Set, n)
		for i := 0; i < n; i++ {
			s[fmt.Sprintf("k%06d", i)] = rec("f", doc.Num(float64(i)))
		}
		return s
	}
	_, _, st := Merge(mk(), mk(), mk())
	if st.Lookups > 4*n {
		t.Errorf("查找次数 %d 超过上界 %d", st.Lookups, 4*n)
	}
}

func TestReportAccesses(t *testing.T) {
	anc := doc.Set{"a": rec("f", doc.Num(1)), "b": rec("f", doc.Num(1)), "c": rec("f", doc.Num(1))}
	left := doc.Set{"a": rec("f", doc.Num(2)), "c": rec("f", doc.Num(9))}
	right := doc.Set{"a": rec("f", doc.Num(3)), "b": rec("f", doc.Num(2)), "c": rec("f", doc.Num(9))}
	_, rep, st := Merge(anc, left, right)
	if st.ReportAccesses != len(rep.List) || len(rep.List) != 2 {
		t.Errorf("报告访问 %d 次, 冲突 %d 条", st.ReportAccesses, len(rep.List))
	}
}

func TestContradictoryDelta(t *testing.T) {
	d := delta.New()
	d.Deleted["k1"] = true
	d.Changed["k1"] = map[string]delta.FieldChange{"f": {New: doc.Num(1), NewOK: true}}
	if err := d.Validate(); !errors.Is(err, delta.ErrContradictory) {
		t.Errorf("矛盾变更集未检出: %v", err)
	}
	ok := delta.Compute(doc.Set{"k": rec("f", doc.Num(1))}, doc.Set{"k": rec("f", doc.Num(2))})
	if err := ok.Validate(); err != nil {
		t.Errorf("正常变更集误报: %v", err)
	}
	if len(ok.Changed["k"]) != 1 {
		t.Errorf("Compute 应产生 1 个字段变更: %v", ok.Changed)
	}
}
