package merge3

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/conflict"
	"ontology/delta"
	"ontology/doc"
	"ontology/serialize"
)

type mergeCase struct {
	name      string
	ancestor  doc.Set
	left      doc.Set
	right     doc.Set
	want      doc.Set
	wantKinds []conflict.Kind // 期望冲突类别（按键、字段序）
	wantTypes [2]string       // 非空时校验首个冲突的两侧类型名
}

func mergeCases() []mergeCase {
	return []mergeCase{
		{"左改右未改", doc.Set{"R": {"f": 1}}, doc.Set{"R": {"f": 2}}, doc.Set{"R": {"f": 1}},
			doc.Set{"R": {"f": 2}}, nil, [2]string{}},
		{"两侧同值", doc.Set{"R": {"f": 1}}, doc.Set{"R": {"f": 2}}, doc.Set{"R": {"f": 2}},
			doc.Set{"R": {"f": 2}}, nil, [2]string{}},
		{"两侧异值", doc.Set{"R": {"f": 1}}, doc.Set{"R": {"f": 2}}, doc.Set{"R": {"f": 3}},
			doc.Set{"R": {"f": 2}}, []conflict.Kind{conflict.FieldValue}, [2]string{}},
		{"左删右改", doc.Set{"R": {"f": 1}}, doc.Set{}, doc.Set{"R": {"f": 9}},
			doc.Set{}, []conflict.Kind{conflict.DeleteModify}, [2]string{}},
		{"两侧同删", doc.Set{"R": {"f": 1}}, doc.Set{}, doc.Set{},
			doc.Set{}, nil, [2]string{}},
		{"改不同字段", doc.Set{"R": {"a": 1, "b": 1, "c": 1}},
			doc.Set{"R": {"a": 2, "b": 1, "c": 1}},
			doc.Set{"R": {"a": 1, "b": 3, "c": 1}},
			doc.Set{"R": {"a": 2, "b": 3, "c": 1}}, nil, [2]string{}},
		{"同键新增同内容", doc.Set{}, doc.Set{"N": {"f": 1}}, doc.Set{"N": {"f": 1}},
			doc.Set{"N": {"f": 1}}, nil, [2]string{}},
		{"同键新增异内容", doc.Set{}, doc.Set{"N": {"f": 1}}, doc.Set{"N": {"f": 2}},
			doc.Set{"N": {"f": 1}}, []conflict.Kind{conflict.AddAdd}, [2]string{}},
		{"祖先缺失视为新增", doc.Set{"A": {"x": 0}},
			doc.Set{"A": {"x": 0}, "N": {"f": 1}}, doc.Set{"A": {"x": 0}, "N": {"f": 1}},
			doc.Set{"A": {"x": 0}, "N": {"f": 1}}, nil, [2]string{}},
		{"类型不一致", doc.Set{"R": {"f": 1}}, doc.Set{"R": {"f": "s"}}, doc.Set{"R": {"f": 2}},
			doc.Set{"R": {"f": "s"}}, []conflict.Kind{conflict.TypeMismatch}, [2]string{"string", "int"}},
		{"三集合皆空", doc.Set{}, doc.Set{}, doc.Set{},
			doc.Set{}, nil, [2]string{}},
		{"祖先为空两侧有内容", doc.Set{}, doc.Set{"L": {"f": 1}}, doc.Set{"R": {"f": 2}},
			doc.Set{"L": {"f": 1}, "R": {"f": 2}}, nil, [2]string{}},
		{"某侧为空即删除全部", doc.Set{"A": {"f": 1}, "B": {"f": 1}},
			doc.Set{}, doc.Set{"A": {"f": 1}, "B": {"f": 2}},
			doc.Set{}, []conflict.Kind{conflict.DeleteModify}, [2]string{}},
		{"空串键合法", doc.Set{"": {"f": 1}}, doc.Set{"": {"f": 2}}, doc.Set{"": {"f": 1}},
			doc.Set{"": {"f": 2}}, nil, [2]string{}},
		{"空串字段名合法", doc.Set{"R": {"": 1}}, doc.Set{"R": {"": 2}}, doc.Set{"R": {"": 1}},
			doc.Set{"R": {"": 2}}, nil, [2]string{}},
		{"空串值与字段不存在可区分", doc.Set{"R": {"f": "x"}}, doc.Set{"R": {"f": ""}}, doc.Set{"R": {}},
			doc.Set{"R": {"f": ""}}, []conflict.Kind{conflict.FieldValue}, [2]string{}},
	}
}

func setEqual(a, b doc.Set) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ra := range a {
		rb, ok := b[k]
		if !ok || !doc.Equal(ra, rb) {
			return false
		}
	}
	return true
}

func TestMergeTable(t *testing.T) {
	for _, tc := range mergeCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, cs := Merge(tc.ancestor, tc.left, tc.right)
			if !setEqual(got, tc.want) {
				t.Fatalf("merged = %v, want %v", got, tc.want)
			}
			if len(cs) != len(tc.wantKinds) {
				t.Fatalf("conflicts = %v, want kinds %v", cs, tc.wantKinds)
			}
			for i, k := range tc.wantKinds {
				if cs[i].Kind != k {
					t.Fatalf("conflict[%d].Kind = %v, want %v", i, cs[i].Kind, k)
				}
			}
			if tc.wantTypes[0] != "" {
				if cs[0].LeftType != tc.wantTypes[0] || cs[0].RightType != tc.wantTypes[1] {
					t.Fatalf("types = %s/%s, want %v", cs[0].LeftType, cs[0].RightType, tc.wantTypes)
				}
				rep := conflict.Report(cs)
				if !strings.Contains(rep, tc.wantTypes[0]) || !strings.Contains(rep, tc.wantTypes[1]) {
					t.Fatalf("report missing types: %q", rep)
				}
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	build := func(seed int64, f func(k string) (doc.Record, bool)) doc.Set {
		perm := rand.New(rand.NewSource(seed)).Perm(len(keys))
		s := make(doc.Set)
		for _, i := range perm {
			if rec, ok := f(keys[i]); ok {
				s[keys[i]] = rec
			}
		}
		return s
	}
	anc := func(k string) (doc.Record, bool) { return doc.Record{"a": 1, "b": "x"}, true }
	left := func(k string) (doc.Record, bool) {
		return doc.Record{"a": 2, "b": "x"}, k != "k4"
	}
	right := func(k string) (doc.Record, bool) {
		return doc.Record{"a": 1, "b": "y"}, k != "k5"
	}
	var base []byte
	for seed := int64(0); seed < 20; seed++ {
		m, cs := Merge(build(seed, anc), build(seed+100, left), build(seed+200, right))
		out := serialize.Encode(m, cs)
		if seed == 0 {
			base = out
		} else if !bytes.Equal(base, out) {
			t.Fatalf("seed %d produced different bytes", seed)
		}
	}
}

func TestLookupBound(t *testing.T) {
	const n = 50000
	mk := func(v int) doc.Set {
		s := make(doc.Set, n)
		for i := 0; i < n; i++ {
			s[fmt.Sprintf("k%06d", i)] = doc.Record{"f": v}
		}
		return s
	}
	Merge(mk(1), mk(2), mk(3))
	if got, bound := LookupCount(), 4*n; got > bound {
		t.Fatalf("lookups = %d, want <= %d", got, bound)
	}
}

func TestReportAccessCount(t *testing.T) {
	_, cs := Merge(
		doc.Set{"A": {"f": 1}, "B": {"f": 1}, "C": {"f": 1}},
		doc.Set{"A": {"f": 2}, "B": {"f": 2}},
		doc.Set{"A": {"f": 3}, "B": {"f": 1}, "C": {"f": 9}},
	)
	conflict.Report(cs)
	if got := conflict.ReportAccess(); got != len(cs) {
		t.Fatalf("report access = %d, want %d (conflict count)", got, len(cs))
	}
}

func TestContradictoryDelta(t *testing.T) {
	cases := []struct {
		name    string
		d       delta.Delta
		wantErr bool
		key     string
	}{
		{"删除又修改", delta.Delta{"K": {Deleted: true, Set: map[string]any{"a": 1}}}, true, "K"},
		{"删除又删字段", delta.Delta{"K": {Deleted: true, Unset: map[string]bool{"a": true}}}, true, "K"},
		{"删除又新增", delta.Delta{"K": {Deleted: true, Added: true}}, true, "K"},
		{"仅删除", delta.Delta{"K": {Deleted: true}}, false, ""},
		{"仅修改", delta.Delta{"K": {Set: map[string]any{"a": 1}}}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := delta.Validate(tc.d)
			if tc.wantErr {
				if !errors.Is(err, delta.ErrContradictory) {
					t.Fatalf("err = %v, want ErrContradictory", err)
				}
				if !strings.Contains(err.Error(), `"`+tc.key+`"`) {
					t.Fatalf("err %v missing key %q", err, tc.key)
				}
			} else if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
		})
	}
}
