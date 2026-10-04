package slot_test

import (
	"testing"

	"ontology/slot"
)

func TestFNV1a32KnownVectors(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
	}{
		{"", 0x811c9dc5},
		{"a", 0xe40c292c},
		{"foobar", 0xbf9cf968},
	}
	for _, tc := range cases {
		if got := slot.FNV1a32([]byte(tc.in)); got != tc.want {
			t.Fatalf("FNV1a32(%q)=%#08x want %#08x", tc.in, got, tc.want)
		}
	}
}

func TestSlot(t *testing.T) {
	cases := []struct {
		name    string
		hid, hr uint32
		R, P    int
		want    int
	}{
		{"spec example h=13", 5, 13, 8, 1, 5},
		{"P=1 offset ignored", 42, 13, 8, 1, 5},
		{"P=3 offset", 5, 6, 8, 3, 0},
		{"P=3 no offset", 3, 6, 8, 3, 6},
		{"near 2^32 no wraparound", uint32(1), uint32(0xFFFFFFFE), 16, 3, 15},
		{"near 2^32 offset max", uint32(2), uint32(0xFFFFFFFF), 1024, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slot.Slot(tc.hid, tc.hr, tc.R, tc.P); got != tc.want {
				t.Fatalf("Slot(%d,%d,R=%d,P=%d)=%d want %d", tc.hid, tc.hr, tc.R, tc.P, got, tc.want)
			}
		})
	}
}

func TestShard(t *testing.T) {
	cases := []struct {
		slotValue, N, R, want int
	}{
		{5, 2, 8, 1},
		{2, 4, 8, 1},
		{5, 8, 8, 5},
		{0, 4, 8, 0},
	}
	for _, tc := range cases {
		if got := slot.Shard(tc.slotValue, tc.N, tc.R); got != tc.want {
			t.Fatalf("Shard(s=%d,N=%d,R=%d)=%d want %d", tc.slotValue, tc.N, tc.R, got, tc.want)
		}
	}
}

func TestSearchSlots(t *testing.T) {
	if got := slot.SearchSlots(6, 8, 3); len(got) != 3 || got[0] != 6 || got[1] != 7 || got[2] != 0 {
		t.Fatalf("wrap-around slots = %v want [6 7 0]", got)
	}
	if got := slot.SearchSlots(13, 8, 1); len(got) != 1 || got[0] != 5 {
		t.Fatalf("P=1 slots = %v want [5]", got)
	}
}
