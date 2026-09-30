package ontology

import (
	"bytes"
	"testing"
)

func TestBuildRejections(t *testing.T) {
	cases := []struct {
		name   string
		assets []AssetSpec
		edges  []EdgeSpec
		reason Reason
	}{
		{"unknown upstream", []AssetSpec{{"A", 0, 1}}, []EdgeSpec{{"X", "A", 0, 0}}, ReasonUnknownAsset},
		{"unknown downstream", []AssetSpec{{"A", 0, 1}}, []EdgeSpec{{"A", "X", 0, 0}}, ReasonUnknownAsset},
		{"duplicate edge", []AssetSpec{{"A", 0, 1}, {"B", 0, 1}},
			[]EdgeSpec{{"A", "B", 0, 0}, {"A", "B", 0, 0}}, ReasonDuplicateEdge},
		{"self dependency cycle", []AssetSpec{{"A", 0, 1}}, []EdgeSpec{{"A", "A", 0, 0}}, ReasonCycle},
		{"multi node cycle", []AssetSpec{{"A", 0, 1}, {"B", 0, 1}},
			[]EdgeSpec{{"A", "B", 0, 0}, {"B", "A", 0, 0}}, ReasonCycle},
		{"lo greater than hi", []AssetSpec{{"A", 0, 1}, {"B", 0, 1}},
			[]EdgeSpec{{"A", "B", 1, 0}}, ReasonBadOffset},
		{"bad range", []AssetSpec{{"A", 2, 1}}, nil, ReasonBadRange},
		{"duplicate asset", []AssetSpec{{"A", 0, 1}, {"A", 0, 1}}, nil, ReasonDuplicateAsset},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.assets, c.edges, WithLogOutput(&bytes.Buffer{}))
			if reasonOf(err) != c.reason {
				t.Fatalf("reason=%v want=%v err=%v", reasonOf(err), c.reason, err)
			}
		})
	}
}

func TestOperationRejections(t *testing.T) {
	p, _ := basicGraph(t)

	if reasonOf(p.ExternalWrite("A", 1)) != ReasonExternalOnDerived {
		t.Fatal("external write on derived asset must be rejected")
	}
	if reasonOf(p.ExternalWrite("S", 100)) != ReasonPartitionOutside {
		t.Fatal("out-of-range partition must be rejected")
	}
	if reasonOf(p.ExternalWrite("Nope", 0)) != ReasonUnknownAsset {
		t.Fatal("unknown asset must be rejected")
	}
	if _, err := p.Start("A", 50); reasonOf(err) != ReasonPartitionOutside {
		t.Fatalf("start out-of-range: %v", err)
	}
	if _, err := p.Start("A", 1); reasonOf(err) != ReasonInputNotReady {
		t.Fatalf("missing input must block: %v", err)
	}
	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}
	run := mustStart(t, p, "A", 1)
	if _, err := p.Start("A", 1); reasonOf(err) != ReasonAlreadyRunning {
		t.Fatalf("second running start: %v", err)
	}
	if reasonOf(p.Complete(999, true)) != ReasonRunNotFound {
		t.Fatal("missing run must be rejected")
	}
	if err := p.Complete(run, true); err != nil {
		t.Fatal(err)
	}
	if reasonOf(p.Complete(run, true)) != ReasonRunFinished {
		t.Fatal("recompleting finished run must be rejected")
	}
	if _, err := p.Plan([]PartitionRef{{"S", 99}}); reasonOf(err) != ReasonPartitionOutside {
		t.Fatalf("plan out-of-range: %v", err)
	}
	if _, err := p.Impact("Nope", 0); reasonOf(err) != ReasonUnknownAsset {
		t.Fatalf("impact unknown asset: %v", err)
	}
}

func TestRejectedOperationChangesNothing(t *testing.T) {
	p, _ := basicGraph(t)
	before := len(p.parts)
	_ = p.ExternalWrite("A", 1)
	_ = p.ExternalWrite("S", 100)
	_, _ = p.Start("A", 1)
	if len(p.parts) != before {
		t.Fatalf("rejected ops created state: before=%d after=%d", before, len(p.parts))
	}
}
