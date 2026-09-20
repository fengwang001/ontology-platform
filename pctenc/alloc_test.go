package pctenc

import "testing"

func TestEncodeNoAllocWhenClean(t *testing.T) {
	clean := "Already_Clean.123~"
	allocs := testing.AllocsPerRun(100, func() {
		if Encode(clean, Query) != clean {
			t.Fatal("unexpected encoding")
		}
	})
	if allocs != 0 {
		t.Errorf("Encode on clean string allocated %v times, want 0", allocs)
	}
}

func TestDecodeNoAllocWithoutEscapes(t *testing.T) {
	plain := "plain:string+with&no&escapes"
	allocs := testing.AllocsPerRun(100, func() {
		got, err := Decode(plain, Path)
		if err != nil || got != plain {
			t.Fatalf("Decode = %q, %v", got, err)
		}
	})
	if allocs != 0 {
		t.Errorf("Decode without escapes allocated %v times, want 0", allocs)
	}
}
