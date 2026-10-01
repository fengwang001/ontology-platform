package regbank

import (
	"errors"
	"testing"
)

// mixDef 在一个 32 位寄存器内放下全部六种访问类型，24..31 位为未覆盖空洞。
func mixDef() RegDef {
	return RegDef{
		Name: "MIX",
		Fields: []FieldDef{
			{Name: "rw", Lo: 0, Width: 4, Access: RW, Reset: 0x5},
			{Name: "ro", Lo: 4, Width: 4, Access: RO, Reset: 0xA},
			{Name: "wo", Lo: 8, Width: 4, Access: WO, Reset: 0x3},
			{Name: "w1c", Lo: 12, Width: 4, Access: W1C, Reset: 0xF},
			{Name: "w1s", Lo: 16, Width: 4, Access: W1S, Reset: 0x0},
			{Name: "rc", Lo: 20, Width: 4, Access: RC, Reset: 0x7},
		},
	}
}

func mustBank(t *testing.T, defs []RegDef) *Bank {
	t.Helper()
	b, err := New(defs)
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	return b
}

// TestAccessTable 覆盖六种访问类型在复位、读、全 1 写、再读下的完整读写表。
func TestAccessTable(t *testing.T) {
	b := mustBank(t, []RegDef{mixDef()})

	raw, err := b.Raw("MIX")
	if err != nil || raw != 0x0070F3A5 {
		t.Fatalf("reset Raw = %#08x, err=%v, want 0x0070f3a5", raw, err)
	}
	t.Logf("input=Raw(MIX) after reset | output=%#08x | verdict=各位域复位值拼成的字(WO 也可见)", raw)

	r1, err := b.Read("MIX")
	if err != nil {
		t.Fatal(err)
	}
	if r1 != 0x0070F0A5 {
		t.Fatalf("Read #1 = %#08x, want 0x0070f0a5", r1)
	}
	t.Logf("input=Read(MIX) #1 | output=%#08x | verdict=WO 位读 0、RC 位返回清空前的 7", r1)

	rawAfterRead, _ := b.Raw("MIX")
	if rawAfterRead != 0x0000F3A5 {
		t.Fatalf("Raw after RC read = %#08x, want 0x0000f3a5", rawAfterRead)
	}
	t.Logf("input=Raw(MIX) | output=%#08x | verdict=RC 已清 0，WO 存值 3 仍在", rawAfterRead)

	r2, _ := b.Read("MIX")
	if r2 != 0x0000F0A5 {
		t.Fatalf("Read #2 = %#08x, want 0x0000f0a5", r2)
	}
	t.Logf("input=Read(MIX) #2 | output=%#08x | verdict=连续第二次读 RC 位为 0", r2)

	if err := b.Write("MIX", 0xFFFFFFFF, 15); err != nil {
		t.Fatal(err)
	}
	rawW, _ := b.Raw("MIX")
	// RW=F, RO 保持 A, WO=F, W1C 写 1 全清=0, W1S 写 1 全置=F, RC 写忽略=0
	if rawW != 0x000F0FAF {
		t.Fatalf("Raw after write-ones = %#08x, want 0x000f0faf", rawW)
	}
	t.Logf("input=Write(MIX,0xffffffff,be=15) Raw=%#08x | verdict=RW/WO 存 F；RO/RC 不变；W1C 清 0；W1S 置 F", rawW)

	r3, _ := b.Read("MIX")
	if r3 != 0x000F00AF {
		t.Fatalf("Read after write-ones = %#08x, want 0x000f00af", r3)
	}
	t.Logf("input=Read(MIX) #3 | output=%#08x | verdict=WO 读仍为 0，其余按存值返回", r3)

	b2 := mustBank(t, []RegDef{mixDef()})
	if err := b2.Write("MIX", 0xFF000000, 15); err != nil {
		t.Fatal(err)
	}
	rawGap, _ := b2.Raw("MIX")
	if rawGap&0xFF000000 != 0 {
		t.Fatalf("uncovered bits stored %#08x, want 0", rawGap)
	}
	t.Logf("input=Write 只写未覆盖位 | Raw=%#08x | verdict=未覆盖位写被忽略", rawGap)
}

// TestW1CW1SZeros 专门验证 W1C/W1S 写 0 不变与选择性写 1。
func TestW1CW1SZeros(t *testing.T) {
	b := mustBank(t, []RegDef{{
		Name: "CS",
		Fields: []FieldDef{
			{Name: "c", Lo: 0, Width: 8, Access: W1C, Reset: 0b10101010},
			{Name: "s", Lo: 8, Width: 8, Access: W1S, Reset: 0b01010101},
		},
	}})
	if err := b.Write("CS", 0x0000, 15); err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.Raw("CS"); raw != 0x55AA {
		t.Fatalf("write zero changed state: %#06x want 0x55aa", raw)
	}
	if err := b.Write("CS", 0x0FFF, 15); err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.Raw("CS"); raw != 0x5F00 {
		t.Fatalf("W1C/W1S selective = %#06x, want 0x5f00", raw)
	}
	t.Logf("input=Write(CS,0x0fff) | Raw=0x5f00 | verdict=W1C 仅写 1 位清零、W1S 仅写 1 位置 1")
}

// TestByteEnable 验证跨字节位域的部分使能规则与 be=0。
func TestByteEnable(t *testing.T) {
	b := mustBank(t, []RegDef{{
		Name: "X",
		Fields: []FieldDef{
			{Name: "span", Lo: 4, Width: 8, Access: RW, Reset: 0xAB},
			{Name: "hi", Lo: 24, Width: 8, Access: RW, Reset: 0x00},
		},
	}})
	if err := b.Write("X", 0x00000FF0, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("X")
	if raw != 0x00AB0 {
		t.Fatalf("partial-byte write changed span: %#010x, want 0x000ab0", raw)
	}
	t.Logf("input=Write(X,0xff0,be=0001) | Raw=%#010x | verdict=span 跨两字节,仅使能其一故整体不变", raw)

	if err := b.Write("X", 0x00000FF0, 2); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("X")
	if raw != 0x00AB0 {
		t.Fatalf("be=2 changed span: %#010x", raw)
	}

	if err := b.Write("X", 0x00000FF0, 3); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("X")
	if raw != 0x00FF0 {
		t.Fatalf("be=3 Raw=%#010x, want 0x00ff0", raw)
	}
	t.Logf("input=Write(X,0xff0,be=0011) | Raw=%#010x | verdict=span 两字节全使能才更新,hi 字节未使能保持 0", raw)

	if err := b.Write("X", 0xFFFFFFFF, 0); err != nil {
		t.Fatal(err)
	}
	raw, _ = b.Raw("X")
	if raw != 0x00FF0 {
		t.Fatalf("be=0 changed state: %#010x", raw)
	}
	t.Logf("input=Write(X,0xffffffff,be=0) | Raw=%#010x | verdict=be=0 无任何效果", raw)
}

// TestRCReadClear 验证 RC 返回旧值并清零，且被拒绝的读不触发清零。
func TestRCReadClear(t *testing.T) {
	b := mustBank(t, []RegDef{mixDef()})
	if _, err := b.Read("MISSING"); !errors.Is(err, ErrNoRegister) {
		t.Fatalf("Read missing err=%v, want ErrNoRegister", err)
	}
	r, _ := b.Read("MIX")
	if r&0x00F00000 != 0x00700000 {
		t.Fatalf("rejected Read must not clear RC: got rc=%#x", r&0x00F00000)
	}
	if err := b.HwSet("MIX", "rc", 0x9); err != nil {
		t.Fatal(err)
	}
	r, _ = b.Read("MIX")
	if r&0x00F00000 != 0x00900000 {
		t.Fatalf("RC after HwSet = %#x", r&0x00F00000)
	}
	r, _ = b.Read("MIX")
	if r&0x00F00000 != 0 {
		t.Fatalf("RC second read = %#x, want 0", r&0x00F00000)
	}
	t.Logf("input=HwSet(rc=9)+Read+Read | verdict=首次读返回 9 并清零,第二次读 0")
}

// TestReadModifyWrite 验证 RMW 对 W1C 的误清、mask 内写 0 不清、对 WO 的误覆盖。
func TestReadModifyWrite(t *testing.T) {
	b := mustBank(t, []RegDef{{
		Name: "R",
		Fields: []FieldDef{
			{Name: "c", Lo: 0, Width: 8, Access: W1C, Reset: 0xFF},
			{Name: "w", Lo: 8, Width: 8, Access: WO, Reset: 0xAB},
			{Name: "d", Lo: 16, Width: 8, Access: RW, Reset: 0x11},
		},
	}})
	// mask 只覆盖 d（RW）：r=0x1100ff（WO 读 0），
	// w=0x2200ff → W1C 被写回的 1 误清；WO 被读回的 0 误覆盖。
	r, stored, err := b.ReadModifyWrite("R", 0x00FF0000, 0x00220000)
	if err != nil {
		t.Fatal(err)
	}
	if r != 0x001100FF || stored != 0x00220000 {
		t.Fatalf("RMW r=%#010x stored=%#010x, want 0x1100ff / 0x220000", r, stored)
	}
	t.Logf("input=RMW(mask=ff0000,value=220000) | output r=%#010x stored=%#010x | verdict=mask 外 W1C 被写回 1 误清、WO 被读回 0 误覆盖", r, stored)

	// mask 覆盖整个 W1C 且 value=0：写 0 不清位。
	b2 := mustBank(t, []RegDef{{
		Name: "C",
		Fields: []FieldDef{
			{Name: "c", Lo: 0, Width: 8, Access: W1C, Reset: 0xFF},
			{Name: "d", Lo: 16, Width: 8, Access: RW, Reset: 0x11},
		},
	}})
	r2, stored2, err := b2.ReadModifyWrite("C", 0x000000FF, 0x00000000)
	if err != nil {
		t.Fatal(err)
	}
	if r2 != 0x001100FF || stored2 != 0x001100FF {
		t.Fatalf("RMW mask-inside-zero r=%#010x stored=%#010x, want 0x1100ff/0x1100ff", r2, stored2)
	}
	t.Logf("input=RMW(mask=ff,value=0) | output stored=%#010x | verdict=mask 内 value=0 的位写 0 不被清", stored2)
}
