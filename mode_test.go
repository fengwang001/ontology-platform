package ontology

import "testing"

func TestModeOrderAndJoin(t *testing.T) {
	if JoinMode(S, IX) != SIX {
		t.Fatalf("JoinMode(S, IX) = %s, want SIX", JoinMode(S, IX))
	}
	if ModeLE(IX, S) || ModeLE(S, IX) {
		t.Fatal("IX and S must be incomparable")
	}
	if JoinMode(IS, X) != X || JoinMode(IX, SIX) != SIX || JoinMode(SIX, X) != X {
		t.Fatal("comparable joins must take the stronger mode")
	}
}

func TestCompatibilityMatrix(t *testing.T) {
	compatible := [][2]Mode{
		{IS, IS}, {IS, IX}, {IS, S}, {IS, SIX},
		{IX, IX}, {S, S},
	}
	for _, pair := range compatible {
		if !CompatibleModes(pair[0], pair[1]) {
			t.Fatalf("%s and %s must be compatible", pair[0], pair[1])
		}
	}

	incompatible := [][2]Mode{
		{IX, S}, {SIX, IX}, {SIX, SIX}, {IS, X}, {IX, X}, {S, X}, {SIX, X}, {X, X},
	}
	for _, pair := range incompatible {
		if CompatibleModes(pair[0], pair[1]) {
			t.Fatalf("%s and %s must be incompatible", pair[0], pair[1])
		}
	}
}
