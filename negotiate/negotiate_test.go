package negotiate_test

import (
	"testing"

	"ontology/caps"
	"ontology/negotiate"
)

func feats() []caps.Feature {
	inf := uint64(1_000_000_001)
	fs := make([]caps.Feature, caps.FeatureCount)
	for i := range fs {
		fs[i] = caps.Feature{MinV: 1, MaxV: inf, Role: 0}
	}
	fs[1] = caps.Feature{MinV: 2, MaxV: 5, Role: 0}
	fs[2] = caps.Feature{MinV: 3, MaxV: inf, Role: 1}
	fs[3] = caps.Feature{MinV: 4, MaxV: inf, Role: 0}
	return fs
}

func TestPickOrderingAndFields(t *testing.T) {
	tab, err := caps.NewTable(feats())
	if err != nil {
		t.Fatal(err)
	}
	out, e := negotiate.Pick(negotiate.Params{
		Table:  tab,
		Client: caps.Hello{Lo: 1, Hi: 6, Sup: 0xf},
		CR:     0,
		Server: caps.Hello{Lo: 2, Hi: 4, Sup: 0xf},
	})
	if e != nil {
		t.Fatal(e)
	}
	if out.Ver != 4 || out.Enabled != 0b1011 || out.L != 2 || out.H != 4 ||
		out.Lower != 2 || out.Upper != 4 || out.Q != 0 {
		t.Fatalf("unexpected outcome: %+v", out)
	}

	// Extra（使用中集合）参与必需窗口：U={1} 把 v 钉在 4。
	out2, e2 := negotiate.Pick(negotiate.Params{
		Table:  tab,
		Client: caps.Hello{Lo: 1, Hi: 6, Sup: 0xf},
		CR:     0,
		Server: caps.Hello{Lo: 2, Hi: 8, Sup: 0xf},
		Extra:  0b0010,
	})
	if e2 != nil {
		t.Fatal(e2)
	}
	if out2.Ver != 4 || out2.Q != 0b0010 {
		t.Fatalf("extra pin failed: %+v", out2)
	}

	// Extra 中特性不被支持 → Missing。
	_, e3 := negotiate.Pick(negotiate.Params{
		Table:  tab,
		Client: caps.Hello{Lo: 1, Hi: 6, Sup: 0b1101},
		CR:     0,
		Server: caps.Hello{Lo: 1, Hi: 6, Sup: 0b1101},
		Extra:  0b0010,
	})
	if got := caps.AsError(e3); got == nil || got.Reason != caps.ReasonMissing || got.Feature != 1 {
		t.Fatalf("want Missing(1), got %v", e3)
	}
}
