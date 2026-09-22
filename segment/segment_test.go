package segment

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

func buildSeg(t *testing.T, opts Options, vals []int64, nulls []bool) *Segment {
	t.Helper()
	b := NewBuilder(opts)
	for i, v := range vals {
		var err error
		if nulls[i] {
			err = b.AddNull()
		} else {
			err = b.Add(v)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readAll(t *testing.T, s *Segment) ([]int64, []bool) {
	t.Helper()
	var vals []int64
	var nulls []bool
	for g := 0; g < s.GroupCount(); g++ {
		rows, err := s.DecodeGroup(g)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			vals = append(vals, r.V)
			nulls = append(nulls, r.Null)
		}
	}
	return vals, nulls
}

func TestRoundTripBothEncodings(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	// 混合：低基数大值（字典）与高基数小值（位打包）交替成行组。
	var vals []int64
	var nulls []bool
	dictVals := []int64{math.MinInt64, -1 << 40, 0, 1 << 40, math.MaxInt64}
	for g := 0; g < 8; g++ {
		for i := 0; i < 100; i++ {
			switch rng.Intn(10) {
			case 0:
				nulls = append(nulls, true)
				vals = append(vals, 0)
			default:
				nulls = append(nulls, false)
				if g%2 == 0 {
					vals = append(vals, dictVals[rng.Intn(len(dictVals))])
				} else {
					vals = append(vals, int64(rng.Intn(1000)-500))
				}
			}
		}
	}
	s := buildSeg(t, Options{RowGroupRows: 100}, vals, nulls)
	gotVals, gotNulls := readAll(t, s)
	if len(gotVals) != len(vals) {
		t.Fatalf("rows=%d want %d", len(gotVals), len(vals))
	}
	for i := range vals {
		if gotNulls[i] != nulls[i] {
			t.Fatalf("row %d null=%v want %v", i, gotNulls[i], nulls[i])
		}
		if !nulls[i] && gotVals[i] != vals[i] {
			t.Fatalf("row %d val=%d want %d", i, gotVals[i], vals[i])
		}
	}
	// 同一段内两种编码都必须出现。
	seen := map[Encoding]bool{}
	for g := 0; g < s.GroupCount(); g++ {
		enc, err := s.GroupEncoding(g)
		if err != nil {
			t.Fatal(err)
		}
		seen[enc] = true
	}
	if !seen[EncDict] || !seen[EncBitpack] {
		t.Fatalf("want both encodings, got %v", seen)
	}
}

func TestExtremeValues(t *testing.T) {
	vals := []int64{math.MinInt64, math.MaxInt64, 0, -1, 1, math.MinInt64 + 1, math.MaxInt64 - 1}
	nulls := make([]bool, len(vals))
	s := buildSeg(t, Options{RowGroupRows: 3}, vals, nulls)
	got, _ := readAll(t, s)
	for i := range vals {
		if got[i] != vals[i] {
			t.Fatalf("row %d got %d want %d", i, got[i], vals[i])
		}
	}
}

func TestAllEqualAndAllNull(t *testing.T) {
	// 全相等。
	vals := make([]int64, 50)
	for i := range vals {
		vals[i] = 42
	}
	s := buildSeg(t, Options{RowGroupRows: 20}, vals, make([]bool, 50))
	got, gotNulls := readAll(t, s)
	for i := range got {
		if got[i] != 42 || gotNulls[i] {
			t.Fatalf("row %d got %d null=%v", i, got[i], gotNulls[i])
		}
	}
	// 全空：统计必须是"无"，读回全部为空。
	b := NewBuilder(Options{RowGroupRows: 20})
	for i := 0; i < 50; i++ {
		if err := b.AddNull(); err != nil {
			t.Fatal(err)
		}
	}
	s2, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for g := 0; g < s2.GroupCount(); g++ {
		st, err := s2.GroupStats(g)
		if err != nil {
			t.Fatal(err)
		}
		if st.HasValue || st.Nulls == 0 {
			t.Fatalf("all-null group stats=%+v", st)
		}
	}
	_, gotNulls2 := readAll(t, s2)
	for i, n := range gotNulls2 {
		if !n {
			t.Fatalf("row %d should be null", i)
		}
	}
}

func TestNullZeroEmptyThreeStates(t *testing.T) {
	// 空值与数值 0 必须是可判定的两种结果。
	b := NewBuilder(Options{RowGroupRows: 8})
	if err := b.AddNull(); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(0); err != nil {
		t.Fatal(err)
	}
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.DecodeGroup(0)
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Null {
		t.Fatal("row 0 must be null")
	}
	if rows[1].Null || rows[1].V != 0 {
		t.Fatal("row 1 must be non-null zero")
	}
	// 统计不得把空值算进去：min/max 只来自非空的 0。
	st, err := s.GroupStats(0)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasValue || st.Min != 0 || st.Max != 0 || st.Nulls != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

func TestLimits(t *testing.T) {
	// 行数超限：拒绝且状态不变。
	b := NewBuilder(Options{RowGroupRows: 4, MaxRows: 3, MaxRowGroups: 100})
	for i := 0; i < 3; i++ {
		if err := b.Add(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Add(99); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err=%v want ErrTooManyRows", err)
	}
	if b.Rows() != 3 {
		t.Fatalf("rows=%d want 3 (state must be unchanged)", b.Rows())
	}
	// 行组数超限：错误类型与行数超限可区分。
	b2 := NewBuilder(Options{RowGroupRows: 2, MaxRows: 100, MaxRowGroups: 2})
	for i := 0; i < 4; i++ {
		if err := b2.Add(int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := b2.Add(99); !errors.Is(err, ErrTooManyRowGroups) {
		t.Fatalf("err=%v want ErrTooManyRowGroups", err)
	}
	if errors.Is(ErrTooManyRows, ErrTooManyRowGroups) || b2.Rows() != 4 {
		t.Fatal("limit errors must be distinguishable and state unchanged")
	}
	if _, err := b2.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDictFallbackToBitpack(t *testing.T) {
	// 字典基数上限 4，但行组有 10 个不同的大值：
	// 必须回退位打包而不是报错，且值无损。
	b := NewBuilder(Options{RowGroupRows: 10, MaxDictCard: 4})
	want := make([]int64, 10)
	for i := range want {
		want[i] = int64(i) * (1 << 40)
		if err := b.Add(want[i]); err != nil {
			t.Fatal(err)
		}
	}
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := s.GroupEncoding(0)
	if err != nil {
		t.Fatal(err)
	}
	if enc != EncBitpack {
		t.Fatalf("enc=%v want bitpack fallback", enc)
	}
	rows, err := s.DecodeGroup(0)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if r.Null || r.V != want[i] {
			t.Fatalf("row %d got %v want %d", i, r, want[i])
		}
	}
}
