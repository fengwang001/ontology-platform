package anomaly

import (
	"reflect"
	"testing"
)

func v(t, n int) [2]int { return [2]int{t, n} }

func wr(key string, t, n int) Op {
	ver := v(t, n)
	return Op{Type: "read", Key: key, ReadVersion: &ver}
}
func ww(key string) Op { return Op{Type: "write", Key: key} }

// 丢失更新：T1、T2 都从 x 的初始版本读出，随后各自写 x。
// 环 T1 --RW--> T2 --WW--> T1，恰好一条 RW，归 G-single（PL-2）。
func TestLostUpdate_GSingle(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x", 0, 0), ww("x")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 0, 0), ww("x")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(1, 1), v(2, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatGSingle || r.Level != LevelPL2 {
		t.Fatalf("got %+v", r)
	}
	if !reflect.DeepEqual(r.Witness.Txns, []int{1, 2}) {
		t.Fatalf("witness = %+v", r.Witness)
	}
	rwCount := 0
	for _, e := range r.Witness.Edges {
		if e == EdgeRW {
			rwCount++
		}
	}
	if rwCount != 1 {
		t.Fatalf("edges = %v", r.Witness.Edges)
	}
}

// 读偏斜：T1 读 x 初始、y 初始；T2 写 x、y。
// 两条 RW 边经一个 T1 的 WW/WR 无关路径汇合：T1 RW->T2，
// 需再构造 T2 到 T1 的非 RW 边才能成环；读偏斜标准形态由
// T1 还读到 T2 前版本给出：这里使用经典只读事务跨键反序，
// 环含恰好一条 RW（x 键），故 G-single。
func TestReadSkew_GSingle(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x", 0, 0), wr("y", 2, 1)}},
			{ID: 2, Status: Committed, Ops: []Op{ww("x"), ww("y")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(2, 1)},
			"y": {v(0, 0), v(2, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatGSingle || r.Level != LevelPL2 {
		t.Fatalf("got %+v", r)
	}
	if !reflect.DeepEqual(r.Witness.Txns, []int{1, 2}) {
		t.Fatalf("witness = %+v", r.Witness)
	}
}

// 写偏斜：T1、T2 都读对方随后写的键，环上有两条 RW 边，归 G2（PL-2+）。
func TestWriteSkew_G2(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x", 0, 0), wr("y", 0, 0), ww("x")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 0, 0), wr("y", 0, 0), ww("y")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(1, 1)},
			"y": {v(0, 0), v(2, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatG2 || r.Level != LevelPL2P {
		t.Fatalf("got %+v", r)
	}
	rwCount := 0
	for _, e := range r.Witness.Edges {
		if e == EdgeRW {
			rwCount++
		}
	}
	if rwCount < 2 {
		t.Fatalf("edges = %v", r.Witness.Edges)
	}
}

// G1a：已提交事务读到已中止事务写的版本。
func TestG1a_AbortedVersion(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Aborted, Ops: []Op{ww("x")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 1, 1)}},
		},
		Order: map[string][][2]int{"x": {v(0, 0)}},
	}
	r := Analyze(h)
	if r.Category != CatG1a || r.Level != LevelPL1 {
		t.Fatalf("got %+v", r)
	}
}

// G1b：已提交事务读到别的事务对该键的非最后一次写版本。
func TestG1b_IntermediateVersion(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{ww("x"), ww("x")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 1, 1)}},
		},
		Order: map[string][][2]int{"x": {v(0, 0), v(1, 2)}},
	}
	r := Analyze(h)
	if r.Category != CatG1b || r.Level != LevelPL1 {
		t.Fatalf("got %+v", r)
	}
}

// G0：纯写写环（跨键），隔离等级为「无」。
func TestG0_WriteWriteCycle(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{ww("x"), ww("y")}},
			{ID: 2, Status: Committed, Ops: []Op{ww("y"), ww("x")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(1, 1), v(2, 1)},
			"y": {v(0, 0), v(2, 1), v(1, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatG0 || r.Level != LevelNone {
		t.Fatalf("got %+v", r)
	}
	for _, e := range r.Witness.Edges {
		if e != EdgeWW {
			t.Fatalf("non-WW edge %v in %v", e, r.Witness)
		}
	}
}

// G1c：仅由 WW/WR 边组成的环（无 RW 边）。
func TestG1c_OnlyWWWR(t *testing.T) {
	// T1 写 x；T2 读 T1 的 x 并写 y；T1 读 T2 的 y。
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{ww("x"), wr("y", 2, 1)}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x", 1, 1), ww("y")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(1, 1)},
			"y": {v(0, 0), v(2, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatG1c || r.Level != LevelPL1 {
		t.Fatalf("got %+v", r)
	}
}

// 多类并存：G0 同时存在时优先报 G0。
func TestPrecedence_G0First(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Aborted, Ops: []Op{ww("z")}},
			{ID: 2, Status: Committed, Ops: []Op{ww("x"), ww("y"), wr("z", 1, 1)}},
			{ID: 3, Status: Committed, Ops: []Op{ww("y"), ww("x")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(2, 1), v(3, 1)},
			"y": {v(0, 0), v(3, 1), v(2, 1)},
			"z": {v(0, 0)},
		},
	}
	r := Analyze(h)
	if r.Category != CatG0 {
		t.Fatalf("got %+v", r)
	}
}

// G1a 优先于 G1b/G1c/G-single/G2。
func TestPrecedence_G1aFirst(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Aborted, Ops: []Op{ww("z")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("z", 1, 1), wr("x", 0, 0), ww("x")}},
			{ID: 3, Status: Committed, Ops: []Op{wr("x", 0, 0), ww("x")}},
		},
		Order: map[string][][2]int{
			"z": {v(0, 0)},
			"x": {v(0, 0), v(2, 1), v(3, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatG1a {
		t.Fatalf("got %+v", r)
	}
}

// 同一历史里同时存在 G-single 环与 G2 环时，先报 G-single。
func TestPrecedence_GSingleBeforeG2(t *testing.T) {
	// 三事务：T1<->T2 构成双 RW 的 G2 环，同时 x 键上安排
	// T1 读初始、T3 紧邻其后给出一条 RW，T3 与 T1 间再用 WR
	// 闭合，构成恰一条 RW 的 G-single 环。
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{
				wr("x", 0, 0), wr("y", 0, 0), wr("z", 3, 1), ww("y"),
			}},
			{ID: 2, Status: Committed, Ops: []Op{
				wr("x", 0, 0), wr("y", 0, 0), ww("x"),
			}},
			{ID: 3, Status: Committed, Ops: []Op{ww("x"), ww("z")}},
		},
		Order: map[string][][2]int{
			"x": {v(0, 0), v(2, 1), v(3, 1)},
			"y": {v(0, 0), v(1, 1)},
			"z": {v(0, 0), v(3, 1)},
		},
	}
	r := Analyze(h)
	if r.Category != CatGSingle {
		t.Fatalf("got %+v", r)
	}
}

func TestClean_PL3(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 7, Status: Committed, Ops: []Op{ww("k"), wr("k", 7, 1)}},
			{ID: 3, Status: Committed, Ops: []Op{wr("k", 7, 1)}},
		},
		Order: map[string][][2]int{"k": {v(0, 0), v(7, 1)}},
	}
	r := Analyze(h)
	if r.Category != CatNoAnomaly || r.Level != LevelPL3 {
		t.Fatalf("got %+v", r)
	}
}

// 拒绝原因的固定顺序。
func TestRejectionOrder(t *testing.T) {
	cases := []struct {
		name   string
		h      History
		reject string
	}{
		{"zero id", History{Txns: []Txn{{ID: 0, Status: Committed}}}, RejectZeroID},
		{"dup id", History{Txns: []Txn{
			{ID: 1, Status: Committed}, {ID: 1, Status: Committed},
		}}, RejectZeroID},
		{"bad read", History{Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x", 9, 1)}},
		}}, RejectBadReadVersion},
		{"order missing initial", History{
			Txns:  []Txn{{ID: 1, Status: Committed, Ops: []Op{ww("x")}}},
			Order: map[string][][2]int{"x": {v(1, 1)}},
		}, RejectBadOrder},
		{"order includes aborted", History{
			Txns: []Txn{
				{ID: 1, Status: Aborted, Ops: []Op{ww("x")}},
			},
			Order: map[string][][2]int{"x": {v(0, 0), v(1, 1)}},
		}, RejectBadOrder},
		{"order includes intermediate", History{
			Txns:  []Txn{{ID: 1, Status: Committed, Ops: []Op{ww("x"), ww("x")}}},
			Order: map[string][][2]int{"x": {v(0, 0), v(1, 1)}},
		}, RejectBadOrder},
		{"order missing committed last", History{
			Txns:  []Txn{{ID: 1, Status: Committed, Ops: []Op{ww("x")}}},
			Order: map[string][][2]int{"x": {v(0, 0)}},
		}, RejectBadOrder},
		{"order duplicate", History{
			Txns:  []Txn{{ID: 1, Status: Committed, Ops: []Op{ww("x")}}},
			Order: map[string][][2]int{"x": {v(0, 0), v(1, 1), v(1, 1)}},
		}, RejectBadOrder},
		{"bad read beats order", History{
			Txns:  []Txn{{ID: 1, Status: Committed, Ops: []Op{wr("x", 9, 1)}}},
			Order: map[string][][2]int{"x": {v(1, 1)}},
		}, RejectBadReadVersion},
		{"too many when otherwise valid", func() History {
			h := History{}
			for i := 1; i <= 13; i++ {
				h.Txns = append(h.Txns, Txn{ID: i, Status: Committed})
			}
			return h
		}(), RejectTooManyTxns},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Analyze(c.h)
			if r.Accepted || r.Reject != c.reject {
				t.Fatalf("got %+v want %s", r, c.reject)
			}
		})
	}
}

// 前序错误优先于「事务数超过 12」。
func TestRejectZeroIDBeatsTooMany(t *testing.T) {
	h := History{}
	for i := 1; i <= 13; i++ {
		h.Txns = append(h.Txns, Txn{ID: i, Status: Committed})
	}
	h.Txns[12].ID = 0
	if r := Analyze(h); r.Reject != RejectZeroID {
		t.Fatalf("got %+v", r)
	}
}
