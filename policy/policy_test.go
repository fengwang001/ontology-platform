package policy

import (
	"testing"

	"ontology/group"
)

func TestEffective(t *testing.T) {
	s := group.New()
	if e := Effective(s, "ghost"); len(e) != 0 {
		t.Fatalf("missing device: want empty, got %v", e)
	}

	apply := func(ops ...group.Op) {
		t.Helper()
		if err := s.Apply(ops, 16); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	apply(
		group.Op{Kind: group.AddDevice, Dev: "d"},
		group.Op{Kind: group.AddGroup, Group: "g1", PR: 10},
		group.Op{Kind: group.AddGroup, Group: "g2", PR: 10},
		group.Op{Kind: group.AddMember, Group: "g1", Dev: "d"},
		group.Op{Kind: group.AddMember, Group: "g2", Dev: "d"},
	)
	s.Apply([]group.Op{
		{Kind: group.SetPolicy, Group: group.Star, Policy: group.Policy{
			"interval": {Val: "300"},
			"log":      {Val: "warn"},
		}},
		{Kind: group.SetPolicy, Group: "g1", Policy: group.Policy{
			"interval": {Val: "30"},
			"mode":     {Val: "eco"},
		}},
		{Kind: group.SetPolicy, Group: "g2", Policy: group.Policy{
			"interval": {Val: "60"},
		}},
	}, 16)

	e := Effective(s, "d")
	want := map[group.Name]string{"interval": "30", "mode": "eco", "log": "warn"}
	if len(e) != len(want) {
		t.Fatalf("tie: got %v want %v", e, want)
	}
	for k, v := range want {
		if e[k] != v {
			t.Fatalf("tie key %s: got %q want %q (full %v)", k, e[k], v, e)
		}
	}

	// 高优先级 Unset 遮蔽：键消失而非回落 "30"。
	apply(
		group.Op{Kind: group.AddGroup, Group: "g3", PR: 20},
		group.Op{Kind: group.AddMember, Group: "g3", Dev: "d"},
		group.Op{Kind: group.SetPolicy, Group: "g3", Policy: group.Policy{"mode": {Unset: true}}},
	)
	e = Effective(s, "d")
	if _, ok := e["mode"]; ok {
		t.Fatalf("Unset must mask mode (no fallback), got %v", e)
	}
	if e["interval"] != "30" || e["log"] != "warn" {
		t.Fatalf("other keys unchanged: %v", e)
	}
	if len(e) != 2 {
		t.Fatalf("want exactly 2 keys, got %v", e)
	}
}

func TestEmptyStringVsUnset(t *testing.T) {
	s := group.New()
	s.Apply([]group.Op{
		{Kind: group.AddDevice, Dev: "d"},
		{Kind: group.AddGroup, Group: "g", PR: 5},
		{Kind: group.AddMember, Group: "g", Dev: "d"},
		{Kind: group.SetPolicy, Group: group.Star, Policy: group.Policy{
			"a": {Val: "star"},
			"b": {Val: "star"},
		}},
		{Kind: group.SetPolicy, Group: "g", Policy: group.Policy{
			"a": {Val: ""},
			"b": {Unset: true},
		}},
	}, 16)

	e := Effective(s, "d")
	v, ok := e["a"]
	if !ok || v != "" {
		t.Fatalf("empty string must appear: a=%q ok=%v", v, ok)
	}
	if _, ok := e["b"]; ok {
		t.Fatalf("Unset key must be absent: %v", e)
	}
}
