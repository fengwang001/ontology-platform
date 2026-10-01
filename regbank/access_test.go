package regbank

import "testing"

func TestAccessTables(t *testing.T) {
	b, err := NewBank(validSpecs())
	if err != nil {
		t.Fatalf("NewBank: %v", err)
	}

	// RW: write stores, read returns it. RO: write ignored, read returns value.
	if err := b.Write("MIX", 0x0000007F, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("MIX")
	t.Logf("input Write(MIX, 0x0000007f, be=15); output Raw=0x%08x; RW byte must be 0x7f, RO byte stays 0xAB", raw)
	if raw&0xff != 0x7f {
		t.Fatalf("RW write: got %#x want 0x7f", raw&0xff)
	}
	if (raw>>8)&0xff != 0xAB {
		t.Fatalf("RO changed by write: %#x", (raw>>8)&0xff)
	}
	r, _ := b.Read("MIX")
	t.Logf("input Read(MIX); output 0x%08x; RO byte reads 0xAB, WO byte reads 0", r)
	if (r>>8)&0xff != 0xAB {
		t.Fatalf("RO read: %#x", (r>>8)&0xff)
	}

	// WO: stored, visible via Raw, reads as 0.
	if err := b.Write("MIX", 0x005A0000, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	r, _ = b.Read("MIX")
	t.Logf("input Write WO byte 0x5A; output Raw=0x%08x Read=0x%08x; Raw shows 0x5A, Read shows 0", raw, r)
	if (raw>>16)&0xff != 0x5A {
		t.Fatalf("WO not stored: %#x", (raw>>16)&0xff)
	}
	if (r>>16)&0xff != 0 {
		t.Fatalf("WO must read as 0: %#x", (r>>16)&0xff)
	}

	// W1C: reset 0xF0; writing 0 keeps, writing 1 clears.
	b.Reset()
	if err := b.Write("MIX", 0x0F000000, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	t.Logf("input W1C write 0x0F against stored 0xF0; output w1c=0x%02x; written-0 bits unchanged", (raw>>24)&0xff)
	if (raw>>24)&0xff != 0xF0 {
		t.Fatalf("W1C write 0 must keep bits: %#x", (raw>>24)&0xff)
	}
	if err := b.Write("MIX", 0xF0000000, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("MIX")
	t.Logf("input W1C write 0xF0; output w1c=0x%02x; written-1 bits clear", (raw>>24)&0xff)
	if (raw>>24)&0xff != 0x00 {
		t.Fatalf("W1C write 1 must clear: %#x", (raw>>24)&0xff)
	}

	// W1S: write 1 sets, write 0 leaves.
	b.Reset()
	if err := b.Write("W1SRC", 0x00000FE0, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("W1SRC")
	t.Logf("input W1S write 0xFE0 against 0x001; output ws=0x%03x want 0xFE1", raw&0xfff)
	if raw&0xfff != 0xFE1 {
		t.Fatalf("W1S: %#x", raw&0xfff)
	}
	// Writing 0 to already-zero bits and 1 to already-set bits leaves it.
	if err := b.Write("W1SRC", 0x00000FE1, 0xf); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("W1SRC")
	if raw&0xfff != 0xFE1 {
		t.Fatalf("W1S write 0 must leave bits: %#x", raw&0xfff)
	}
}

func TestRCReadClears(t *testing.T) {
	b, _ := NewBank(validSpecs())
	r1, err := b.Read("W1SRC")
	if err != nil {
		t.Fatal(err)
	}
	raw1, _ := b.Raw("W1SRC")
	r2, _ := b.Read("W1SRC")
	t.Logf("input Read(W1SRC) twice; output read1=0x%08x rawAfter=0x%08x read2=0x%08x; first read pre-clear 0x1FF, second 0", r1, raw1, r2)
	if (r1>>16)&0x1ff != 0x1ff {
		t.Fatalf("RC first read must show pre-clear value: %#x", (r1>>16)&0x1ff)
	}
	if (raw1>>16)&0x1ff != 0 {
		t.Fatalf("RC must be cleared after read: %#x", raw1)
	}
	if (r2>>16)&0x1ff != 0 {
		t.Fatalf("RC second read must be 0: %#x", (r2>>16)&0x1ff)
	}
	if r2&0xfff != 0x001 {
		t.Fatalf("non-RC field changed by Read: %#x", r2&0xfff)
	}
}

func TestByteEnable(t *testing.T) {
	b, _ := NewBank(validSpecs())

	before, _ := b.Raw("MIX")
	if err := b.Write("MIX", 0xFFFFFFFF, 0); err != nil {
		t.Fatal(err)
	}
	after, _ := b.Raw("MIX")
	t.Logf("input Write(MIX, all-ones, be=0); output before=0x%08x after=0x%08x; no enabled byte means no effect", before, after)
	if before != after {
		t.Fatal("be=0 must have no effect")
	}

	// 'cross' spans bits 4..11 (bytes 0 and 1): partial enables keep it whole.
	before, _ = b.Raw("STRADDLE")
	for _, be := range []int{0x1, 0x2} {
		// Keep low nibble at its reset value so byte-0-aligned 'lo' is a
		// no-op; only the cross-byte field is under inspection.
		if err := b.Write("STRADDLE", 0x00000FF1, be); err != nil {
			t.Fatal(err)
		}
		after, _ = b.Raw("STRADDLE")
		cross := (after >> 4) & 0xff
		t.Logf("input cross-byte field write be=%d; output raw before=0x%08x after=0x%08x cross=0x%02x; field not wholly enabled -> unchanged", be, before, after, cross)
		if cross != 0x22 {
			t.Fatalf("cross-byte field must stay unchanged for partial be=%d", be)
		}
	}
	// The 4-bit 'lo' field only touches byte 0: be=1 updates it.
	if err := b.Write("STRADDLE", 0x0000000F, 0x1); err != nil {
		t.Fatal(err)
	}
	after, _ = b.Raw("STRADDLE")
	t.Logf("input be=1 writes 4-bit 'lo' field; output lo=0x%x want 0xf", after&0xf)
	if after&0xf != 0xf {
		t.Fatalf("byte-0-only field should update with be=1: %#x", after&0xf)
	}
	// Both bytes enabled updates 'cross'.
	if err := b.Write("STRADDLE", 0x00000FF0, 0x3); err != nil {
		t.Fatal(err)
	}
	after, _ = b.Raw("STRADDLE")
	t.Logf("input be=3 writes cross field 0xFF; output cross=0x%02x", (after>>4)&0xff)
	if (after>>4)&0xff != 0xff {
		t.Fatalf("cross field should update with both bytes enabled: %#x", (after>>4)&0xff)
	}

	// Enabling only byte 3 still updates the byte-aligned W1C field.
	b.Reset()
	if err := b.Write("MIX", 0xF0000000, 0x8); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("MIX")
	t.Logf("input be=8 W1C write 0xF0; output w1c=0x%02x want 0x00", (raw>>24)&0xff)
	if (raw>>24)&0xff != 0 {
		t.Fatal("aligned field must update when its sole byte is enabled")
	}
}
