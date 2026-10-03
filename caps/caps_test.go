package caps_test

import (
	"testing"

	"ontology/caps"
)

func validFeats() []caps.Feature {
	fs := make([]caps.Feature, caps.FeatureCount)
	for i := range fs {
		fs[i] = caps.Feature{MinV: 1, MaxV: 1_000_000_001, Role: 0}
	}
	return fs
}

func TestNewTableValidation(t *testing.T) {
	if _, err := caps.NewTable(validFeats()[:31]); err == nil {
		t.Fatal("want error for 31 features")
	}
	cases := []caps.Feature{
		{MinV: 0, MaxV: 1_000_000_001, Role: 0},
		{MinV: 1_000_000_001, MaxV: 1_000_000_001, Role: 0},
		{MinV: 5, MaxV: 5, Role: 0},
		{MinV: 1, MaxV: 1_000_000_002, Role: 0},
		{MinV: 1, MaxV: 2, Role: 3},
	}
	for i, bad := range cases {
		fs := validFeats()
		fs[0] = bad
		if _, err := caps.NewTable(fs); err == nil {
			t.Fatalf("case %d: want invalid table", i)
		}
	}
}

func TestHelloValidationAndBits(t *testing.T) {
	bad := []caps.Hello{
		{Lo: 0, Hi: 1},
		{Lo: 2, Hi: 1_000_000_001},
		{Lo: 5, Hi: 4},
		{Lo: 1, Hi: 1, Sup: 0b00, Req: 0b01},
	}
	for i, h := range bad {
		if err := caps.ValidateHello(h); err == nil {
			t.Fatalf("hello %d should be invalid: %+v", i, h)
		}
	}
	if err := caps.ValidateHello(caps.Hello{Lo: 1, Hi: 1, Sup: 0b101, Req: 0b101}); err != nil {
		t.Fatal(err)
	}

	tab, _ := caps.NewTable(validFeats())
	if !tab.AvailableAt(0, 1) || tab.AvailableAt(0, 1_000_000_001) {
		t.Fatal("sentinel boundary wrong")
	}
	if got := caps.Bits(0b10101); len(got) != 3 || got[0] != 0 || got[1] != 2 || got[2] != 4 {
		t.Fatalf("Bits=%v", got)
	}
	if _, ok := tab.At(32); ok {
		t.Fatal("At(32) must fail")
	}
}
