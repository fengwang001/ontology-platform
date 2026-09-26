// Package api 是对外门面：解析、序列化与自检。
package api

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"ontology/lex"
	"ontology/parse"
)

type Value = parse.Value

type API struct{}

func New() *API { return &API{} }

func (API) ParseText(s string) (Value, error) { return parse.Parse([]byte(s)) }

func (API) Stringify(v Value) (string, error) {
	if v.Kind == parse.Num && (math.IsNaN(v.Num) || math.IsInf(v.Num, 0)) {
		return "", errors.New("api: non-finite number")
	}
	return v.String(), nil
}

// SelfCheck 对内置文本核验四条不变量：往返一致、严格拒绝、
// ParseNumber 与参照值一致、失败不留痕。全部通过返回 nil。
func (a API) SelfCheck() error {
	goods := []string{
		`null`, `true`, `false`, `0`, `-0.5`, `1e3`, `"a\nb"`, `"😀"`,
		`[1, "x", null, {"k": [true, false]}]`,
		`{"a": 1, "b": [1,2,3], "c": {"d": "e"}}`,
	}
	for _, s := range goods { // 不变量1：往返一致
		v, err := a.ParseText(s)
		if err != nil {
			return fmt.Errorf("selfcheck parse %q: %w", s, err)
		}
		out, err := a.Stringify(v)
		if err != nil {
			return fmt.Errorf("selfcheck stringify %q: %w", s, err)
		}
		v2, err := a.ParseText(out)
		if err != nil || v2.String() != out {
			return fmt.Errorf("selfcheck roundtrip %q", s)
		}
	}
	bads := []struct {
		s   string
		err error
	}{ // 不变量2：严格拒绝，哨兵互不相同
		{"01", lex.ErrLeadingZero}, {"1.", lex.ErrEmptyFrac},
		{"1e", lex.ErrEmptyExp}, {"1e+", lex.ErrEmptyExp},
		{"-", lex.ErrLoneMinus}, {`"\x"`, lex.ErrBadEscape},
		{`"\u12"`, lex.ErrBadEscape}, {`"\uD800"`, lex.ErrLoneSurrogate},
		{`"\uDC00"`, lex.ErrLoneSurrogate}, {`{"a":1,"a":2}`, parse.ErrDupKey},
	}
	for _, b := range bads {
		v, err := a.ParseText(b.s)
		if !errors.Is(err, b.err) || !reflect.DeepEqual(v, Value{}) {
			return fmt.Errorf("selfcheck reject %q: got %v", b.s, err)
		}
	}
	nums := []struct { // 不变量3：ParseNumber 与参照值一致
		s string
		w float64
	}{{"0", 0}, {"-0.5", -0.5}, {"1e3", 1000}, {"3.14159", 3.14159}, {"1e-3", 0.001}}
	for _, c := range nums {
		if got, err := lex.ParseNumber(c.s); err != nil || got != c.w {
			return fmt.Errorf("selfcheck number %q", c.s)
		}
	}
	if _, err := a.ParseText("01"); err == nil { // 不变量4：失败不留痕
		return errors.New("selfcheck: 01 should fail")
	}
	if _, err := a.ParseText(`{"ok": [1, 2]}`); err != nil {
		return fmt.Errorf("selfcheck reuse: %w", err)
	}
	return nil
}
