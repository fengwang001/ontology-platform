package parse

import (
	"errors"
	"fmt"
	"math"
	"ontology/lex"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func naiveNum(s string) float64 {
	return map[bool]float64{true: -1, false: 1}[s[0] == '-'] *
		naiveMag(s[map[bool]int{true: 1, false: 0}[s[0] == '-']:])
}
func naiveMag(s string) float64 {
	digits := func(k int) (float64, int) {
		v := 0.0
		for ; k < len(s) && s[k] >= '0' && s[k] <= '9'; k++ {
			v = v*10 + float64(s[k]-'0')
		}
		return v, k
	}
	v, i := digits(0)
	if i < len(s) && s[i] == '.' {
		f, j := digits(i + 1)
		v, i = v+f/math.Pow10(j-i-1), j
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		e, k := digits(j)
		if s[i+1] == '-' {
			e = -e
		}
		v, i = v*math.Pow10(int(e)), k
	}
	return v
}
func TestNumberVectors(t *testing.T) {
	for _, s := range []string{"0", "-0.5", "1e3", "1.25", "100", "-1.5E2", "0.0", "3.5e-1", "2e2"} {
		got, err := lex.ParseNumber(s)
		if err != nil || got != naiveNum(s) {
			t.Errorf("num %q = %v,%v want %v", s, got, err, naiveNum(s))
		}
	}
}
func TestParseNumberNaiveConsistency(t *testing.T) {
	for i := 0; i < 300; i++ { // generated valid vectors across scales
		s := fmt.Sprintf("%d.%de%d", i, i*13%97, i%5-2)
		if got, _ := lex.ParseNumber(s); got != naiveNum(s) {
			t.Fatalf("%q %v != naive %v", s, got, naiveNum(s))
		}
	}
}
func TestStringVectors(t *testing.T) {
	ok := [][2]string{{`"a\nb"`, "a\nb"}, {`"😀"`, "😀"}, {`"😀"`, "😀"}, {`"A"`, "A"}, {`"x\/y"`, "x/y"}, {`""`, ""}}
	for _, c := range ok {
		got, err := lex.ParseString([]byte(c[0]))
		if err != nil || got != c[1] {
			t.Errorf("str %q = %q,%v want %q", c[0], got, err, c[1])
		}
	}
	for _, c := range []struct {
		in string
		e  error
	}{{`"\q"`, lex.ErrBadEscape}, {`"\u12"`, lex.ErrLoneUnicode}, {`"\uD800"`, lex.ErrLoneSurrogate}, {`"a\uDC00b"`, lex.ErrLoneSurrogate}} {
		if _, err := lex.ParseString([]byte(c.in)); !errors.Is(err, c.e) {
			t.Errorf("str %q err=%v want %v", c.in, err, c.e)
		}
	}
	em, _ := Parse([]byte(`"😀"`))
	if r := []rune(em.Str); len(r) != 1 || r[0] != 0x1F600 {
		t.Errorf("pair not merged to U+1F600: %v", em.Str)
	}
}
func TestRejectSentinels(t *testing.T) {
	ins := []string{"01", "1.", "1e", "-", `"\q"`, `"\u12"`, `"\uD800"`, `{"a":1,"a":2}`}
	want := []error{lex.ErrLeadingZero, lex.ErrEmptyFrac, lex.ErrEmptyExp, lex.ErrLoneMinus,
		lex.ErrBadEscape, lex.ErrLoneUnicode, lex.ErrLoneSurrogate, ErrDuplicateKey}
	seen := map[error]bool{}
	for i, in := range ins {
		v, err := Parse([]byte(in))
		if !errors.Is(err, want[i]) || !reflect.DeepEqual(v, Value{}) || seen[err] {
			t.Errorf("%q: err=%v partial=%v reused=%v", in, err, v, seen[err])
		}
		seen[err] = true
	}
}
func TestRoundTrip(t *testing.T) {
	for _, tx := range []string{`null`, `true`, `0`, `-0.5`, `1e3`, `"a\nb"`, `"😀"`, `[]`, `{}`,
		`[1,2,[3,false],null]`, `{"a":1,"b":[true,null],"c":"x"}`, ` {"k":[{"n":-2.5e2},{}]} `} {
		v, err := Parse([]byte(tx))
		if err != nil {
			t.Fatalf("%q: %v", tx, err)
		}
		v2, err := Parse([]byte(v.String()))
		if err != nil || !reflect.DeepEqual(v, v2) {
			t.Errorf("roundtrip mismatch for %q -> %s", tx, v.String())
		}
	}
}
func TestFailureLeavesNoTrace(t *testing.T) {
	for _, tx := range []string{`{"a":1,"a":2}`, `01`, `"\uD800"`, `[1,`, `tru`} {
		if v, err := Parse([]byte(tx)); err == nil || !reflect.DeepEqual(v, Value{}) {
			t.Errorf("%q: partial value %v returned", tx, v)
		}
	}
	if _, err := Parse([]byte(`{"ok":true}`)); err != nil {
		t.Errorf("parser unusable after failure: %v", err)
	}
}
func TestDupCheckLinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		p := make([]string, m)
		for i := range p {
			p[i] = fmt.Sprintf(`"k%d":%d`, i, i)
		}
		_, cmp, err := parseCounted([]byte("{" + strings.Join(p, ",") + "}"))
		if err != nil || cmp != m { // exactly one O(1) hash probe per distinct key
			t.Errorf("m=%d cmp=%d err=%v, want %d (linear; a scan would be ~m*m/2)", m, cmp, err, m)
		}
	}
}
func TestConcurrent(t *testing.T) {
	const tx = `{"x":[1,true,null,"s"],"y":-2.5e1}`
	want, _ := Parse([]byte(tx))
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			v, err := Parse([]byte(tx)) // same read-only text
			own := Value{Kind: KindArray, Arr: []Value{{Kind: KindNumber, Num: float64(g)}}}
			if err != nil || !reflect.DeepEqual(v, want) || own.String() != fmt.Sprintf("[%d]", g) {
				atomic.AddInt32(&bad, 1)
			}
		}(g)
	}
	wg.Wait()
	if bad != 0 {
		t.Fatalf("%d goroutines disagreed with the serial result", bad)
	}
}
