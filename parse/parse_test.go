package parse_test

import (
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/fmtf"
	"ontology/parse"
)

func TestErrors(t *testing.T) {
	cases := []struct {
		s    string
		want error
		pos  int // 仅 ErrSyntax 时校验字节位置，-1 不校验
	}{
		{"", parse.ErrEmpty, -1},
		{"abc", parse.ErrSyntax, 0},
		{"1x", parse.ErrSyntax, 1},
		{"12.3.4", parse.ErrSyntax, 4},
		{"1e", parse.ErrSyntax, 2},
		{"-", parse.ErrSyntax, 1},
		{".", parse.ErrSyntax, 0},
		{"1e9999999999", parse.ErrRange, -1},
		{"1e500", parse.ErrRange, -1},
		{"1e309", parse.ErrRange, -1},
		{"0.1000000000000000055511151231257827", parse.ErrTooManyDigits, -1},
		{"1.23456789012345678", parse.ErrTooManyDigits, -1},
	}
	for _, c := range cases {
		_, err := parse.Parse(c.s)
		if !errors.Is(err, c.want) {
			t.Errorf("Parse(%q) 错误 = %v，应可判定为 %v", c.s, err, c.want)
		}
		if c.pos >= 0 && !strings.Contains(err.Error(), strconv.Itoa(c.pos)) {
			t.Errorf("Parse(%q) 错误 %v 未给出字节位置 %d", c.s, err, c.pos)
		}
	}
}

func TestParseBasic(t *testing.T) {
	cases := []struct {
		s    string
		want float64
	}{
		{"0", 0.0},
		{"0.1", 0.1},
		{"1", 1.0},
		{"-2.5", -2.5},
		{"1e+17", 1e17},
		{"1.5e-05", 1.5e-5},
		{"1.7976931348623157e+308", math.MaxFloat64},
		{"5e-324", math.SmallestNonzeroFloat64},
		{"9007199254740992", 1 << 53},
	}
	for _, c := range cases {
		got, err := parse.Parse(c.s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.s, err)
		}
		if math.Float64bits(got) != math.Float64bits(c.want) {
			t.Errorf("Parse(%q) = %v，want %v", c.s, got, c.want)
		}
	}
	if got, _ := parse.Parse("-0"); math.Float64bits(got) != math.Float64bits(math.Copysign(0, -1)) {
		t.Error("Parse(-0) 应保留符号位")
	}
}

func sigCount(s string) int {
	s = strings.TrimPrefix(s, "-")
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, ".", "")
	s = strings.TrimLeft(s, "0")
	return len(strings.TrimRight(s, "0"))
}

// TestRoundtripRandom 十万个随机 float64：往返逐位相同，且位数不多于标准库。
func TestRoundtripRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20260924))
	n, longer := 0, 0
	for n < 100000 {
		x := math.Float64frombits(r.Uint64())
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		n++
		s, err := fmtf.Format(x)
		if err != nil {
			t.Fatalf("Format(%v): %v", x, err)
		}
		y, err := parse.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q)（来自 %v）: %v", s, x, err)
		}
		if math.Float64bits(y) != math.Float64bits(x) {
			t.Fatalf("往返失败：%v -> %q -> %v", x, s, y)
		}
		ref := strconv.FormatFloat(x, 'g', -1, 64)
		if sigCount(s) > sigCount(ref) {
			longer++
			t.Errorf("%v: 本实现 %q（%d 位）长于标准库 %q（%d 位）",
				x, s, sigCount(s), ref, sigCount(ref))
		}
	}
	if longer > 0 {
		t.Fatalf("共 %d 个值比标准库更长", longer)
	}
}

// TestSubnormal 次正规数抽样往返。
func TestSubnormal(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	vals := []uint64{1, 2, 3, 0xfffffffffffff, 0x8000000000000, 0x1000000000000}
	for i := 0; i < 2000; i++ {
		vals = append(vals, r.Uint64()&(1<<52-1))
	}
	for _, b := range vals {
		x := math.Float64frombits(b)
		s, err := fmtf.Format(x)
		if err != nil {
			t.Fatalf("Format(%v): %v", x, err)
		}
		y, err := parse.Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if math.Float64bits(y) != b {
			t.Fatalf("次正规数往返失败：%v -> %q -> %v", x, s, y)
		}
	}
}

func TestConcurrentParse(t *testing.T) {
	texts := []string{"0.1", "-2.5", "1e+17", "1e-05", "1.7976931348623157e+308", "5e-324"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				s := texts[(g+i)%len(texts)]
				a, err := parse.Parse(s)
				if err != nil {
					t.Error(err)
					return
				}
				b, _ := parse.Parse(s)
				if math.Float64bits(a) != math.Float64bits(b) {
					t.Errorf("并发下 Parse(%q) 结果不稳定", s)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
