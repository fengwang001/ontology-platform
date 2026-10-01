package regbank

import (
	"errors"
	"testing"
)

// TestLayoutErrors 覆盖全部布局拒绝情形，且必须是同一个错误 ErrLayout。
func TestLayoutErrors(t *testing.T) {
	good := FieldDef{Name: "f", Lo: 0, Width: 4, Access: RW, Reset: 1}
	cases := []struct {
		name string
		defs []RegDef
	}{
		{"empty register name", []RegDef{{Name: "", Fields: []FieldDef{good}}}},
		{"duplicate register name", []RegDef{
			{Name: "R", Fields: []FieldDef{good}},
			{Name: "R", Fields: []FieldDef{{Name: "g", Lo: 0, Width: 1, Access: RW}}},
		}},
		{"empty field name", []RegDef{{Name: "R", Fields: []FieldDef{
			{Lo: 0, Width: 4, Access: RW},
		}}}},
		{"duplicate field name", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 0, Width: 2, Access: RW},
			{Name: "f", Lo: 2, Width: 2, Access: RW},
		}}}},
		{"negative lo", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: -1, Width: 4, Access: RW},
		}}}},
		{"width zero", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 0, Width: 0, Access: RW},
		}}}},
		{"negative width", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 0, Width: -1, Access: RW},
		}}}},
		{"lo+width exceeds 32", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 30, Width: 3, Access: RW},
		}}}},
		{"lo 32 width 1", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 32, Width: 1, Access: RW},
		}}}},
		{"overlap", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "a", Lo: 0, Width: 4, Access: RW},
			{Name: "b", Lo: 3, Width: 4, Access: RW},
		}}}},
		{"reset too big", []RegDef{{Name: "R", Fields: []FieldDef{
			{Name: "f", Lo: 0, Width: 4, Access: RW, Reset: 16},
		}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.defs)
			if !errors.Is(err, ErrLayout) {
				t.Fatalf("New err=%v, want ErrLayout", err)
			}
			t.Logf("input=%s | output=ErrLayout | verdict=布局非法统一拒绝,无部分登记", tc.name)
		})
	}

	// 一个寄存器非法时，先前合法的寄存器也不得出现在 Bank 中。
	b, err := New([]RegDef{
		{Name: "OK", Fields: []FieldDef{good}},
		{Name: "BAD", Fields: []FieldDef{{Name: "f", Lo: 0, Width: 0, Access: RW}}},
	})
	if !errors.Is(err, ErrLayout) || b != nil {
		t.Fatalf("partial registration must fail atomically: b=%v err=%v", b, err)
	}
}

// TestAccessErrors 覆盖运行期错误与「寄存器不存在 → be/位域 → v 越界」的优先级。
func TestAccessErrors(t *testing.T) {
	b := mustBank(t, []RegDef{{Name: "R", Fields: []FieldDef{
		{Name: "f", Lo: 0, Width: 4, Access: RW, Reset: 1},
	}}})

	check := func(name string, want error, fn func() error) {
		t.Helper()
		if err := fn(); !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want %v", name, err, want)
		}
		t.Logf("input=%s | output=%v | verdict=整体拒绝且不改存值", name, want)
	}

	before, _ := b.Raw("R")

	check("write missing reg", ErrNoRegister, func() error { return b.Write("NO", 0, 99) })
	check("be=16", ErrInvalidBE, func() error { return b.Write("R", 0, 16) })
	check("be=-1", ErrInvalidBE, func() error { return b.Write("R", 0, -1) })
	check("hwset missing reg beats field", ErrNoRegister,
		func() error { return b.HwSet("NO", "NOFIELD", 999) })
	check("hwset missing field beats range", ErrNoField,
		func() error { return b.HwSet("R", "NOFIELD", 999) })
	check("hwset v out of range", ErrValueRange,
		func() error { return b.HwSet("R", "f", 16) })
	check("raw missing reg", ErrNoRegister, func() error {
		_, err := b.Raw("NO")
		return err
	})
	check("read missing reg", ErrNoRegister, func() error {
		_, err := b.Read("NO")
		return err
	})
	check("rmw missing reg beats anything", ErrNoRegister, func() error {
		_, _, err := b.ReadModifyWrite("NO", 0, 0)
		return err
	})

	after, _ := b.Raw("R")
	if before != after {
		t.Fatalf("rejected ops changed storage: %#x -> %#x", before, after)
	}
}

// TestResetAndHwSet 验证复位与硬件侧写入（包括 RO 只能被 HwSet/Reset 改变）。
func TestResetAndHwSet(t *testing.T) {
	b := mustBank(t, []RegDef{{Name: "R", Fields: []FieldDef{
		{Name: "ro", Lo: 0, Width: 8, Access: RO, Reset: 0x12},
		{Name: "rw", Lo: 8, Width: 8, Access: RW, Reset: 0x34},
	}}})
	if err := b.Write("R", 0xFFFF, 15); err != nil {
		t.Fatal(err)
	}
	raw, _ := b.Raw("R")
	if raw != 0x00FF12 {
		t.Fatalf("RO must ignore write: %#06x want 0x00ff12", raw)
	}
	if err := b.HwSet("R", "ro", 0xAB); err != nil {
		t.Fatal(err)
	}
	if raw, _ := b.Raw("R"); raw != 0x00FFAB {
		t.Fatalf("HwSet on RO: %#06x want 0x00ffab", raw)
	}
	b.Reset()
	if raw, _ := b.Raw("R"); raw != 0x3412 {
		t.Fatalf("Reset: %#06x want 0x3412", raw)
	}
	t.Logf("input=Write+HwSet(ro)+Reset | verdict=RO 写忽略、HwSet 可改、Reset 复位")
}
