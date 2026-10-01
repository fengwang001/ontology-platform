package regbank

import (
	"errors"
	"testing"
)

func TestLayoutErrors(t *testing.T) {
	good := validSpecs()

	cases := []struct {
		name  string
		specs []RegSpec
	}{
		{"empty register name", []RegSpec{{Name: "", Fields: good[0].Fields}}},
		{"duplicate register name", []RegSpec{good[0], good[0]}},
		{"empty field name", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "", Lo: 0, W: 1, Access: RW, Reset: 0},
		}}}},
		{"duplicate field name", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 0, W: 1, Access: RW, Reset: 0},
			{Name: "a", Lo: 1, W: 1, Access: RW, Reset: 0},
		}}}},
		{"negative lo", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: -1, W: 1, Access: RW, Reset: 0},
		}}}},
		{"width below 1", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 0, W: 0, Access: RW, Reset: 0},
		}}}},
		{"lo+w exceeds 32", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 31, W: 2, Access: RW, Reset: 0},
		}}}},
		{"overlapping fields", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 0, W: 8, Access: RW, Reset: 0},
			{Name: "b", Lo: 7, W: 2, Access: RW, Reset: 0},
		}}}},
		{"reset out of range", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 0, W: 4, Access: RW, Reset: 0x10},
		}}}},
		{"unknown access type", []RegSpec{{Name: "R", Fields: []FieldSpec{
			{Name: "a", Lo: 0, W: 4, Access: "XX", Reset: 0},
		}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewBank(tc.specs)
			if !errors.Is(err, ErrLayout) {
				t.Fatalf("input layout %q; output err=%v; want ErrLayout (rejected, bank never created)", tc.name, err)
			}
			t.Logf("input layout %q; output err=%v; verdict layout rejected with unified error", tc.name, err)
		})
	}
}

func TestAccessErrorOrderAndAtomicity(t *testing.T) {
	b, err := NewBank(validSpecs())
	if err != nil {
		t.Fatal(err)
	}

	// Register existence is always first.
	if _, err := b.Read("NOPE"); !errors.Is(err, ErrUnknownReg) {
		t.Fatalf("Read missing reg: %v", err)
	}
	if err := b.Write("NOPE", 0, 16); !errors.Is(err, ErrUnknownReg) {
		t.Fatalf("Write missing reg must report reg before bad be: %v", err)
	}
	if _, err := b.ReadModifyWrite("NOPE", 0, 0); !errors.Is(err, ErrUnknownReg) {
		t.Fatalf("RMW missing reg: %v", err)
	}
	if err := b.HwSet("NOPE", "rw0", 0); !errors.Is(err, ErrUnknownReg) {
		t.Fatalf("HwSet missing reg must report reg first: %v", err)
	}
	if _, err := b.Raw("NOPE"); !errors.Is(err, ErrUnknownReg) {
		t.Fatalf("Raw missing reg: %v", err)
	}
	t.Log("input accesses on missing register (incl. bad be/field/v); output ErrUnknownReg; verdict reg checked first")

	// be validation comes next, only for Write.
	for _, be := range []int{-1, 16, 31} {
		before, _ := b.Raw("MIX")
		err := b.Write("MIX", 0xFFFFFFFF, be)
		if !errors.Is(err, ErrBadBE) {
			t.Fatalf("be=%d: want ErrBadBE, got %v", be, err)
		}
		after, _ := b.Raw("MIX")
		t.Logf("input Write(MIX, ones, be=%d); output err=%v raw before=0x%08x after=0x%08x; verdict rejected, no state change", be, err, before, after)
		if before != after {
			t.Fatalf("rejected be=%d changed state", be)
		}
	}

	// Field existence before value range, only for HwSet.
	if err := b.HwSet("MIX", "ghost", 0); !errors.Is(err, ErrUnknownFld) {
		t.Fatalf("missing field: %v", err)
	}
	if err := b.HwSet("MIX", "ghost", 0x999); !errors.Is(err, ErrUnknownFld) {
		t.Fatalf("missing field must be reported before v range: %v", err)
	}
	if err := b.HwSet("MIX", "rw0", 0x100); !errors.Is(err, ErrValueRange) {
		t.Fatalf("v out of range: %v", err)
	}
	t.Log("input HwSet with bad field and/or v; output order ErrUnknownFld then ErrValueRange; verdict ordering enforced")

	// A rejected Read must not clear RC fields.
	if _, err := b.Read("MISSING"); !errors.Is(err, ErrUnknownReg) {
		t.Fatal(err)
	}
	raw, _ := b.Raw("W1SRC")
	t.Logf("input rejected Read on missing register; output W1SRC raw=0x%08x; rc still 0x1FF, no RC side effect", raw)
	if (raw>>16)&0x1ff != 0x1ff {
		t.Fatalf("rejected Read triggered RC clear: %#x", raw)
	}
}
