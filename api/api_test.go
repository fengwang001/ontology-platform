package api

import (
	"errors"
	"testing"

	"ontology/pow"
)

// 对外接口结果与八行表一致。
func TestPowModThroughAPI(t *testing.T) {
	c := New()
	cases := []struct{ b, e, m, want int64 }{
		{-2, 3, 5, 2}, {-3, 2, 7, 2}, {-3, 3, 7, 1}, {7, 100, 13, 9},
		{-5, 3, 9, 1}, {2, 0, 5, 1}, {5, 0, 1, 0}, {2, 10, 1, 0},
	}
	for _, tc := range cases {
		got, err := c.PowMod(tc.b, tc.e, tc.m)
		if err != nil || got != tc.want {
			t.Errorf("PowMod(%d,%d,%d)=(%d,%v) want %d", tc.b, tc.e, tc.m, got, err, tc.want)
		}
	}
}

// 对外接口透传三类可判定错误。
func TestErrorsThroughAPI(t *testing.T) {
	c := New()
	cases := []struct {
		b, e, m int64
		want    error
	}{
		{1, 1, 0, pow.ErrZeroModulus},
		{1, 1, -2, pow.ErrNegativeModulus},
		{1, -2, 7, pow.ErrNegativeExponent},
	}
	for _, tc := range cases {
		if _, err := c.PowMod(tc.b, tc.e, tc.m); !errors.Is(err, tc.want) {
			t.Errorf("PowMod(%d,%d,%d) err=%v want %v", tc.b, tc.e, tc.m, err, tc.want)
		}
	}
}

// 内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
