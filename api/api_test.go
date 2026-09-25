package api_test

import (
	"errors"
	"testing"

	"ontology/api"
	"ontology/pow"
)

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestPowModThroughAPI(t *testing.T) {
	a := api.New()
	table := []struct{ b, e, m, want int64 }{
		{-2, 3, 5, 2}, {-3, 2, 7, 2}, {-3, 3, 7, 1}, {7, 100, 13, 9},
		{-5, 3, 9, 1}, {2, 0, 5, 1}, {5, 0, 1, 0}, {2, 10, 1, 0},
	}
	for _, c := range table {
		got, err := a.PowMod(c.b, c.e, c.m)
		if err != nil || got != c.want {
			t.Fatalf("PowMod(%d,%d,%d)=%d,%v want %d", c.b, c.e, c.m, got, err, c.want)
		}
	}
}

func TestAPIErrorsAreSentinels(t *testing.T) {
	a := api.New()
	if _, err := a.PowMod(1, 1, 0); !errors.Is(err, pow.ErrModZero) {
		t.Fatalf("mod==0: %v", err)
	}
	if _, err := a.PowMod(1, 1, -3); !errors.Is(err, pow.ErrModNegative) {
		t.Fatalf("mod<0: %v", err)
	}
	if _, err := a.PowMod(1, -3, 5); !errors.Is(err, pow.ErrExpNegative) {
		t.Fatalf("exp<0: %v", err)
	}
}
