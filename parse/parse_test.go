package parse

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/lex"
)

func naive(s string) float64 { // 教科书参照：逐字符扫描 int/frac/exp 并累加
	i, sign, v := 0, 1.0, 0.0
	if s[i] == '-' {
		sign, i = -1, 1
	}
	dig := func() bool { return i < len(s) && s[i] >= '0' && s[i] <= '9' }
	for dig() {
		v, i = v*10+float64(s[i]-'0'), i+1
	}
	if i < len(s) && s[i] == '.' {
		i++
		for sc := 0.1; dig(); sc *= 0.1 {
			v, i = v+float64(s[i]-'0')*sc, i+1
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		es := 1
		if s[i] == '-' {
			es, i = -1, i+1
		} else if s[i] == '+' {
			i++
		}
		e := 0
		for dig() {
			e, i = e*10+int(s[i]-'0'), i+1
		}
		v *= math.Pow10(es * e)
	}
	return sign * v
}

func TestParseNumberVsNaive(t *testing.T) {
	vecs := []string{"0", "-0", "1", "42", "0.5", "-0.5", "0.25", "0.125", "2.5", "1e3", "1E3", "1e+3", "1e-3", "1e22", "1e-22"}
	for k := 0; k <= 500; k++ {
		vecs = append(vecs, strconv.Itoa(k))
	}
	for k := 1; k <= 60; k++ {
		vecs = append(vecs, fmt.Sprintf("%d.5", k), fmt.Sprintf("%d.25", k), fmt.Sprintf("%de%d", k%9+1, k%15+1))
	}
	for _, s := range vecs {
		if got, err := lex.ParseNumber(s); err != nil || got != naive(s) {
			t.Fatalf("%s: got %v err=%v, naive=%v", s, got, err, naive(s))
		}
	}
}

func TestStrictReject(t *testing.T) {
	type tc struct {
		in  string
		err error
	}
	cases := []tc{
		{"01", lex.ErrLeadingZero}, {"-01", lex.ErrLeadingZero}, {"1.", lex.ErrEmptyFrac}, {"1.e3", lex.ErrEmptyFrac},
		{"1e", lex.ErrEmptyExp}, {"1e+", lex.ErrEmptyExp}, {"-", lex.ErrLoneMinus},
		{`"\x"`, lex.ErrBadEscape}, {`"\u"`, lex.ErrBadEscape}, {`"\u12zz"`, lex.ErrBadEscape},
		{`"\uD800"`, lex.ErrLoneSurrogate}, {`"\uDC00"`, lex.ErrLoneSurrogate}, {`"\uD800A"`, lex.ErrLoneSurrogate}, {`"\uD800\u0041"`, lex.ErrLoneSurrogate},
		{`{"a":1,"a":2}`, ErrDupKey}, {`{"x":{},"x":{}}`, ErrDupKey},
		{"", ErrSyntax}, {"tru", ErrSyntax}, {"[1,]", ErrSyntax}, {"1 2", ErrSyntax}, {"\"a\x01b\"", lex.ErrControl}, {"1e999", lex.ErrBadNumber},
	}
	for _, c := range cases {
		if v, err := Parse([]byte(c.in)); !errors.Is(err, c.err) || !reflect.DeepEqual(v, Value{}) {
			t.Fatalf("%q: got %v %v", c.in, v, err)
		}
	}
	seen := map[error]int{}
	for _, e := range []error{lex.ErrLeadingZero, lex.ErrEmptyFrac, lex.ErrEmptyExp, lex.ErrLoneMinus,
		lex.ErrBadEscape, lex.ErrLoneSurrogate, lex.ErrControl, lex.ErrBadNumber, ErrDupKey, ErrSyntax} {
		seen[e]++
	}
	if len(seen) != 10 {
		t.Fatal("哨兵错误不互异")
	}
}

func TestRoundtrip(t *testing.T) {
	texts := []string{
		`null`, `true`, `false`, `0`, `-0.5`, `1e3`, `0.3`, `3.14159`, `""`, `"a\nb"`,
		`"😀"`, `"A"`, `[]`, `{}`, `[1,"x",null,{"k":[true,false]}]`,
		`{"a":1,"b":[1,2,3],"c":{"d":"e"}}`, `[ 1 , 2 ]`, `{ "k" : 1 }`,
	}
	for _, s := range texts {
		v1, err1 := Parse([]byte(s))
		v2, err2 := Parse([]byte(v1.String()))
		if err1 != nil || err2 != nil || !reflect.DeepEqual(v1, v2) {
			t.Fatalf("%q 往返不一致", s)
		}
	}
}

func TestNoPartial(t *testing.T) {
	for _, s := range []string{`{"a":1,"a":2}`, `{"a":[1,2,}`, `[1,{"b":2},01]`, `["x","\uD800"]`} {
		if v, err := Parse([]byte(s)); err == nil || !reflect.DeepEqual(v, Value{}) {
			t.Fatalf("%q 未整体失败", s)
		}
	}
	if _, err := Parse([]byte(`{"ok":[1,2]}`)); err != nil {
		t.Fatal("失败后不可继续正常使用")
	}
}

func TestDupKeyCmpSubquadratic(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		var sb strings.Builder
		for i := 0; i < m; i++ {
			fmt.Fprintf(&sb, `,"k%d":%d`, i, i)
		}
		p := &parser{b: []byte("{" + sb.String()[1:] + "}")}
		if v, err := p.value(); err != nil || len(v.Obj) != m {
			t.Fatalf("m=%d: %v", m, err)
		}
		if p.totalCmp > 3*m || p.lastCmp > 3 {
			t.Fatalf("m=%d 判重比较 total=%d last=%d，随 m 非线性", m, p.totalCmp, p.lastCmp)
		}
	}
}

func TestConcurrent(t *testing.T) {
	src := `{"s":"😀","a":[0,1.5,-2e3],"o":{"k":null,"t":true}}`
	want, _ := Parse([]byte(src))
	const N = 32
	vals, outs := make([]Value, N), make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); vals[i], _ = Parse([]byte(src)) }(i)
		go func(i int) { defer wg.Done(); outs[i] = Value{Kind: Num, Num: float64(i)}.String() }(i)
	}
	wg.Wait()
	for i := 0; i < N; i++ {
		if w := (Value{Kind: Num, Num: float64(i)}).String(); !reflect.DeepEqual(vals[i], want) || outs[i] != w {
			t.Fatalf("并发结果 %d 不一致", i)
		}
	}
}
