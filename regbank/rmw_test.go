package regbank

import (
	"errors"
	"testing"
)

func TestReadModifyWritePitfalls(t *testing.T) {
	b, _ := NewBank(validSpecs())

	// W1C bits read as 1 but outside mask are written back as 1 -> cleared.
	if _, err := b.ReadModifyWrite("MIX", 0x000000FF, 0x00000099); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("MIX")
	t.Logf("input RMW mask=0x000000ff value=0x99 on reset MIX; output raw=0x%08x; w1c outside mask read 1 -> written 1 -> cleared; rw0 becomes 0x99", raw)
	if raw&0xff != 0x99 {
		t.Fatalf("masked RW field: %#x", raw&0xff)
	}
	if (raw>>24)&0xff != 0x00 {
		t.Fatalf("W1C outside mask must be cleared by read-back: %#x", (raw>>24)&0xff)
	}

	// With the W1C bit inside mask and value 0 there, it survives.
	b.Reset()
	res, err := b.ReadModifyWrite("MIX", 0xF0000000, 0x00000000)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	t.Logf("input RMW mask=0xF0000000 value=0; output read=0x%08x raw=0x%08x; masked value 0 means W1C keeps 0xF0", res.Read, raw)
	if (raw>>24)&0xff != 0xF0 {
		t.Fatalf("W1C in mask with value 0 must not clear: %#x", (raw>>24)&0xff)
	}

	// WO outside mask: the read 0 is written back -> stored value overwritten.
	b.Reset()
	if err := b.HwSet("MIX", "woc", 0x66); err != nil {
		t.Fatal(err)
	}
	res, err = b.ReadModifyWrite("MIX", 0x000000FF, 0x12)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	t.Logf("input RMW with WO stored 0x66, mask excludes WO; output read=0x%08x raw=0x%08x; WO overwritten with read-back 0", res.Read, raw)
	if (raw>>16)&0xff != 0 {
		t.Fatalf("WO outside mask must be overwritten with 0: %#x", (raw>>16)&0xff)
	}
	// WO inside mask gets value's portion.
	b.Reset()
	if _, err := b.ReadModifyWrite("MIX", 0x00FF0000, 0x00770000); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	t.Logf("input RMW mask covers WO with value 0x77; output woc=0x%02x want 0x77", (raw>>16)&0xff)
	if (raw>>16)&0xff != 0x77 {
		t.Fatalf("WO in mask must take value bits: %#x", (raw>>16)&0xff)
	}

	// RMW's internal Read still clears RC before the (ignored) write.
	b.Reset()
	res, err = b.ReadModifyWrite("W1SRC", 0xFFFFFFFF, 0xFFFFFFFF)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("W1SRC")
	t.Logf("input RMW all-ones on W1SRC; output read rc=0x%03x stored rc=0x%03x; RC clears at read, write ignored", (res.Read>>16)&0x1ff, (raw>>16)&0x1ff)
	if (res.Read>>16)&0x1ff != 0x1ff || (raw>>16)&0x1ff != 0 {
		t.Fatal("RC RMW semantics wrong")
	}
}

func TestHwSetRawReset(t *testing.T) {
	b, _ := NewBank(validSpecs())
	if err := b.HwSet("MIX", "ro0", 0x01); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("MIX")
	t.Logf("input HwSet(MIX, ro0, 1); output raw=0x%08x; RO moves only via hardware", raw)
	if (raw>>8)&0xff != 0x01 {
		t.Fatalf("HwSet RO: %#x", (raw>>8)&0xff)
	}
	if err := b.Write("MIX", 0x0000FF00, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	if (raw>>8)&0xff != 0x01 {
		t.Fatalf("bus write must not move RO: %#x", (raw>>8)&0xff)
	}

	// Out-of-range HwSet is rejected and changes nothing.
	err := b.HwSet("MIX", "rw0", 0x100)
	if !errors.Is(err, ErrValueRange) {
		t.Fatalf("want ErrValueRange, got %v", err)
	}

	b.Reset()
	raw, _ = b.Raw("MIX")
	t.Logf("input Reset; output raw=0x%08x; back to reset 0x12/0xAB/0x34/0xF0", raw)
	if raw != 0xF034AB12 {
		t.Fatalf("reset state: %#x", raw)
	}
}
