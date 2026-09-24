package serve_test

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
)

func TestParseTable(t *testing.T) {
	cases := []struct {
		header string
		want   []rangespec.Range
		off    int // >=0 时期望 ParseError 且 Offset==off
	}{
		{"bytes=0-4", []rangespec.Range{{Start: 0, End: 4, Suffix: -1}}, -1},
		{"bytes=5-", []rangespec.Range{{Start: 5, End: -1, Suffix: -1}}, -1},
		{"bytes=-10", []rangespec.Range{{Start: -1, End: -1, Suffix: 10}}, -1},
		{"bytes=-0", []rangespec.Range{{Start: -1, End: -1, Suffix: 0}}, -1},
		{"bytes=0-1,2-3,-4", []rangespec.Range{
			{Start: 0, End: 1, Suffix: -1}, {Start: 2, End: 3, Suffix: -1}, {Start: -1, End: -1, Suffix: 4}}, -1},
		{"bytes=", nil, 6}, {"bytes=0x-1", nil, 7}, {"bytes=-", nil, 6},
		{"bytes=0-1,", nil, 10}, {"bytes=0-1;2-3", nil, 9}, {"text=0-1", nil, 0},
	}
	for _, c := range cases {
		rs, err := rangespec.Parse(c.header)
		if c.off >= 0 {
			var pe *rangespec.ParseError
			if !errors.As(err, &pe) || pe.Offset != c.off {
				t.Errorf("%q: want ParseError@%d, got %v", c.header, c.off, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(rs, c.want) {
			t.Errorf("%q: got %v, %v", c.header, rs, err)
		}
	}
}

func TestClampAndErrorKinds(t *testing.T) {
	const total = 10
	parse := func(h string) []rangespec.Range {
		rs, err := rangespec.Parse(h)
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	cases := []struct {
		header string
		want   []coalesce.Range // nil 表示不可满足
	}{
		{"bytes=5-100", []coalesce.Range{{Start: 5, End: 10}}},                       // b 越界裁到末尾
		{"bytes=-100", []coalesce.Range{{Start: 0, End: 10}}},                        // n 大于总长取全部
		{"bytes=8-9,0-3", []coalesce.Range{{Start: 0, End: 4}, {Start: 8, End: 10}}}, // 排序
		{"bytes=0-3,4-7", []coalesce.Range{{Start: 0, End: 8}}},                      // 相邻合并
		{"bytes=0-5,3-8", []coalesce.Range{{Start: 0, End: 9}}},                      // 重叠合并
		{"bytes=-0", nil}, {"bytes=10-", nil}, {"bytes=5-3", nil},
	}
	for _, c := range cases {
		got, err := coalesce.Normalize(parse(c.header), total)
		if c.want == nil {
			var ue *coalesce.UnsatisfiableError
			var pe *rangespec.ParseError
			if !errors.As(err, &ue) || ue.Total != total || errors.As(err, &pe) {
				t.Errorf("%q: want UnsatisfiableError(total=%d), got %v", c.header, total, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, %v", c.header, got, err)
		}
	}
}

func randSpec(rng *rand.Rand, total int64) rangespec.Range {
	switch rng.Intn(3) {
	case 0:
		return rangespec.Range{Start: rng.Int63n(total + 2), End: rng.Int63n(total + 2), Suffix: -1}
	case 1:
		return rangespec.Range{Start: rng.Int63n(total + 2), End: -1, Suffix: -1}
	default:
		return rangespec.Range{Start: -1, End: -1, Suffix: rng.Int63n(total + 2)}
	}
}

func expand(rs []coalesce.Range, set map[int64]bool) {
	for _, r := range rs {
		for p := r.Start; p < r.End; p++ {
			set[p] = true
		}
	}
}

func TestNormalizeByteSet(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		total := 1 + rng.Int63n(20)
		var specs []rangespec.Range
		before := map[int64]bool{}
		for i := 0; i < 1+rng.Intn(30); i++ {
			s := randSpec(rng, total)
			specs = append(specs, s)
			if one, err := coalesce.Normalize([]rangespec.Range{s}, total); err == nil {
				expand(one, before)
			}
		}
		merged, err := coalesce.Normalize(specs, total)
		after := map[int64]bool{}
		if err == nil {
			expand(merged, after)
			for i := 0; i+1 < len(merged); i++ { // 互不重叠、互不相邻
				if merged[i+1].Start <= merged[i].End {
					t.Fatalf("seed %d: adjacent output %v", seed, merged)
				}
			}
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("seed %d: byte set changed by merge", seed)
		}
	}
}

func comparesBound(n int64) int64 { // n*ceil(log2 n) + (n-1)
	b := int64(0)
	for p := int64(1); p < n; p *= 2 {
		b++
	}
	return n*b + n - 1
}

func TestComparesBound(t *testing.T) {
	var got [2]int64
	for i, n := range []int64{100, 10000} {
		rng := rand.New(rand.NewSource(n))
		specs := make([]rangespec.Range, n)
		for j := range specs {
			a := rng.Int63n(1 << 40)
			specs[j] = rangespec.Range{Start: a, End: a + rng.Int63n(1<<20), Suffix: -1}
		}
		coalesce.ResetCompares()
		_, _ = coalesce.Normalize(specs, 1<<40)
		got[i] = coalesce.Compares()
		if got[i] > comparesBound(n) {
			t.Errorf("n=%d: compares %d exceeds bound %d", n, got[i], comparesBound(n))
		}
	}
	ratio := float64(got[1]) / float64(got[0])
	t.Logf("compares n=100:%d n=10000:%d ratio=%.1f", got[0], got[1], ratio)
	if ratio > 300 { // n log n 增长约 200 余，O(n^2) 约为 100^2=10000
		t.Errorf("ratio %.1f exceeds n log n growth", ratio)
	}
}

func TestBoundaryConflict(t *testing.T) {
	tricky := []byte("x\r\n--ontology-boundary-fake\r\ny")
	ranges := []coalesce.Range{{Start: 0, End: 5}, {Start: 10, End: 15}}
	contents := [][]byte{tricky, []byte("plain")}
	_, boundary, err := multipart.Build("text/plain", 32, ranges, contents, 8)
	if err != nil {
		t.Fatal(err)
	}
	delim := []byte("\r\n--" + boundary)
	if bytes.Count(append(tricky, delim...), delim) != 1 {
		t.Errorf("boundary %q conflicts with content", boundary)
	}
	tries := 0
	gen := func() string { tries++; return "X" }
	bad := [][]byte{[]byte("has \r\n--X inside"), []byte("other")}
	if _, _, err := multipart.BuildWith(gen, "text/plain", 32, ranges, bad, 3); !errors.Is(err, multipart.ErrBoundaryRetries) || tries != 3 {
		t.Errorf("want ErrBoundaryRetries after 3 tries, got %v (tries=%d)", err, tries)
	}
}
