package plan

import (
	"testing"

	"ontology/catalog"
)

type pspec struct {
	l, r string
	ndv  uint64
}

func mkView(order []string, rows map[string]uint64, ps ...pspec) *catalog.View {
	v := &catalog.View{}
	for _, name := range order {
		v.Tables = append(v.Tables, catalog.TableInfo{Name: name, Rows: float64(rows[name])})
	}
	for _, p := range ps {
		ref := func(s string) catalog.ColRef {
			return catalog.ColRef{Table: s[:1], Column: s[2:]}
		}
		v.Preds = append(v.Preds, catalog.PredInfo{
			Pred: catalog.Predicate{Left: ref(p.l), Right: ref(p.r)},
			Sel:  1.0 / float64(p.ndv),
		})
	}
	return v
}

// ulpScenario 的两侧镜像计划数学等代价，但朴素浮点累加差 1 ULP，
// 且名字序更大的计划浮点代价反而更低（见 DESIGN.md 第 2 节）。
func ulpScenario(order []string) *catalog.View {
	rows := map[string]uint64{"A": 2945, "B": 3420, "C": 6660, "D": 3354}
	return mkView(order, rows,
		pspec{"A.x", "B.x", 944}, pspec{"B.y", "C.y", 820}, pspec{"C.z", "D.z", 233})
}

func TestTieBreak(t *testing.T) {
	res, err := Select(ulpScenario([]string{"A", "B", "C", "D"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Best.String(), "(((A⋈B)⋈C)⋈D)"; got != want {
		t.Errorf("got %s, want %s（等代价应选名字序最小者）", got, want)
	}
	// 单元级：1 ULP 之差必须判等，并由名字序列定胜负。
	cases := []struct {
		name       string
		cand, cur  *Plan
		wantBetter bool
	}{
		{"ulp diff, smaller leaf seq wins",
			&Plan{Cost: 130083.61492352212372, leafSeq: "A", shape: "A"},
			&Plan{Cost: 130083.61492352210917, leafSeq: "B", shape: "B"}, true},
		{"ulp diff, larger leaf seq loses",
			&Plan{Cost: 130083.61492352210917, leafSeq: "B", shape: "B"},
			&Plan{Cost: 130083.61492352212372, leafSeq: "A", shape: "A"}, false},
		{"genuinely lower cost wins", &Plan{Cost: 99, leafSeq: "Z", shape: "Z"},
			&Plan{Cost: 100, leafSeq: "A", shape: "A"}, true},
		{"same seq, shape breaks tie", &Plan{Cost: 100, leafSeq: "A", shape: "(A|A)"},
			&Plan{Cost: 100, leafSeq: "A", shape: "((A|A)|A)"}, false},
	}
	for _, tc := range cases {
		if got := better(tc.cand, tc.cur); got != tc.wantBetter {
			t.Errorf("%s: better=%v, want %v", tc.name, got, tc.wantBetter)
		}
	}
}

func TestRegistrationOrderShuffled(t *testing.T) {
	base := []string{"A", "B", "C", "D"}
	res, err := Select(ulpScenario(base))
	if err != nil {
		t.Fatal(err)
	}
	want := res.Best.String()
	// 20 种确定性的登记顺序（轮转 + 反转组合）。
	orders := [][]string{base}
	for i := 1; i < 20; i++ {
		prev := orders[i-1]
		next := []string{prev[3], prev[2], prev[1], prev[0]}
		if i%2 == 0 {
			next = []string{prev[1], prev[2], prev[3], prev[0]}
		}
		orders = append(orders, next)
	}
	for i, order := range orders {
		res, err := Select(ulpScenario(order))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Best.String(); got != want {
			t.Errorf("shuffle %d %v: got %s, want %s", i, order, got, want)
		}
	}
}

func TestCartesianProductPlacement(t *testing.T) {
	rows := map[string]uint64{"A": 1000, "B": 2000, "C": 1500, "D": 500}
	cases := []struct {
		name      string
		preds     []pspec
		wantCross int
		crossLast bool
	}{
		{"chain A-B-C-D has no cross product",
			[]pspec{{"A.x", "B.x", 500}, {"B.y", "C.y", 800}, {"C.z", "D.z", 300}}, 0, false},
		{"disconnected A-B C-D has exactly one cross product at the last step",
			[]pspec{{"A.x", "B.x", 500}, {"C.z", "D.z", 300}}, 1, true},
	}
	for _, tc := range cases {
		res, err := Select(mkView([]string{"A", "B", "C", "D"}, rows, tc.preds...))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Best.CrossCount(); got != tc.wantCross {
			t.Errorf("%s: cross count=%d, want %d (plan %s)", tc.name, got, tc.wantCross, res.Best)
		}
		if tc.crossLast && !res.Best.Cross {
			t.Errorf("%s: root is not the cross product (plan %s)", tc.name, res.Best)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	single, err1 := Select(mkView([]string{"A"}, map[string]uint64{"A": 777}))
	zero, err2 := Select(mkView([]string{"A", "B"}, map[string]uint64{"A": 0, "B": 0},
		pspec{"A.x", "B.x", 10}))
	one, err3 := Select(mkView([]string{"A", "B"}, map[string]uint64{"A": 1, "B": 500},
		pspec{"A.x", "B.x", 10}))
	_, err0 := Select(&catalog.View{})
	cases := []struct {
		name    string
		res     *Result
		err     error
		wantErr bool
		check   func(r *Result) bool
	}{
		{"zero tables is decidable error", nil, err0, true, nil},
		{"single table returns scan with scan cost", single, err1, false,
			func(r *Result) bool { return r.Best.Kind == Scan && r.Best.Cost == 777 && r.Best.Card == 777 }},
		{"all zero rows still deterministic", zero, err2, false,
			func(r *Result) bool { return r.Best.String() == "(A⋈B)" && r.Best.Cost == 0 }},
		{"one-row table joins fine", one, err3, false,
			func(r *Result) bool { return r.Best.Kind == Join && !r.Best.Cross }},
	}
	for _, tc := range cases {
		if (tc.err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, tc.err, tc.wantErr)
		}
		if tc.check != nil && tc.err == nil && !tc.check(tc.res) {
			t.Errorf("%s: check failed (plan %v)", tc.name, tc.res.Best)
		}
	}
}
