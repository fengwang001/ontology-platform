package regbank

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naiveField / naiveBank 是独立于产品代码的「逐位朴素模拟」基线，
// 严格按题目规则逐字段重新实现一遍，供交叉对照。
type naiveField struct {
	name       string
	lo, w      int
	access     Access
	reset, val uint32
}

type naiveReg struct {
	name   string
	fields []naiveField
}

type naiveBank struct {
	regs map[string]*naiveReg
}

func naiveNew(defs []RegDef) (*naiveBank, error) {
	b := &naiveBank{regs: map[string]*naiveReg{}}
	for _, rd := range defs {
		if rd.Name == "" {
			return nil, ErrLayout
		}
		if _, ok := b.regs[rd.Name]; ok {
			return nil, ErrLayout
		}
		seen := map[string]bool{}
		busy := make([]bool, 32)
		var fs []naiveField
		for _, fd := range rd.Fields {
			if fd.Name == "" || seen[fd.Name] {
				return nil, ErrLayout
			}
			if fd.Lo < 0 || fd.Width < 1 || fd.Lo+fd.Width > 32 {
				return nil, ErrLayout
			}
			for i := fd.Lo; i < fd.Lo+fd.Width; i++ {
				if busy[i] {
					return nil, ErrLayout
				}
			}
			max := uint32(1)<<uint(fd.Width) - 1
			if fd.Width == 32 {
				max = ^uint32(0)
			}
			if fd.Reset > max {
				return nil, ErrLayout
			}
			seen[fd.Name] = true
			for i := fd.Lo; i < fd.Lo+fd.Width; i++ {
				busy[i] = true
			}
			fs = append(fs, naiveField{
				name: fd.Name, lo: fd.Lo, w: fd.Width,
				access: fd.Access, reset: fd.Reset, val: fd.Reset,
			})
		}
		b.regs[rd.Name] = &naiveReg{name: rd.Name, fields: fs}
	}
	return b, nil
}

func (b *naiveBank) reset() {
	for _, r := range b.regs {
		for i := range r.fields {
			r.fields[i].val = r.fields[i].reset
		}
	}
}

func fieldEnabled(f *naiveField, be int) bool {
	for i := f.lo; i < f.lo+f.w; i++ {
		if be&(1<<uint(i/8)) == 0 {
			return false
		}
	}
	return true
}

func fieldMask(w int) uint32 {
	if w == 32 {
		return ^uint32(0)
	}
	return uint32(1)<<uint(w) - 1
}

func (b *naiveBank) write(reg string, val uint32, be int) error {
	r, ok := b.regs[reg]
	if !ok {
		return ErrNoRegister
	}
	if be < 0 || be > 15 {
		return ErrInvalidBE
	}
	for i := range r.fields {
		f := &r.fields[i]
		if !fieldEnabled(f, be) {
			continue
		}
		fv := (val >> uint(f.lo)) & fieldMask(f.w)
		switch f.access {
		case RW, WO:
			f.val = fv
		case W1C:
			f.val &^= fv
		case W1S:
			f.val |= fv
		case RO, RC:
		}
	}
	return nil
}

func (b *naiveBank) read(reg string) (uint32, error) {
	r, ok := b.regs[reg]
	if !ok {
		return 0, ErrNoRegister
	}
	var out uint32
	for _, f := range r.fields {
		if f.access != WO {
			out |= f.val << uint(f.lo)
		}
	}
	for i := range r.fields {
		if r.fields[i].access == RC {
			r.fields[i].val = 0
		}
	}
	return out, nil
}

func (b *naiveBank) raw(reg string) (uint32, error) {
	r, ok := b.regs[reg]
	if !ok {
		return 0, ErrNoRegister
	}
	var out uint32
	for _, f := range r.fields {
		out |= f.val << uint(f.lo)
	}
	return out, nil
}

func (b *naiveBank) rmw(reg string, mask, value uint32) (uint32, uint32, error) {
	if _, ok := b.regs[reg]; !ok {
		return 0, 0, ErrNoRegister
	}
	readVal, _ := b.read(reg)
	w := (readVal &^ mask) | (value & mask)
	_ = b.write(reg, w, 15)
	stored, _ := b.raw(reg)
	return readVal, stored, nil
}

func (b *naiveBank) hwSet(reg, fname string, v uint32) error {
	r, ok := b.regs[reg]
	if !ok {
		return ErrNoRegister
	}
	idx := -1
	for i := range r.fields {
		if r.fields[i].name == fname {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrNoField
	}
	if v > fieldMask(r.fields[idx].w) {
		return ErrValueRange
	}
	r.fields[idx].val = v
	return nil
}

// randomDefs 生成随机寄存器定义；约 1/10 概率注入布局错误，交给两个 New 同时拒绝。
func randomDefs(rng *rand.Rand) ([]RegDef, bool) {
	n := 1 + rng.Intn(3)
	defs := make([]RegDef, 0, n)
	intentionallyBad := rng.Intn(10) == 0
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("R%d", i)
		busy := make([]bool, 32)
		fields := []FieldDef{}
		fn := 0
		for place := 0; place < 1+rng.Intn(6); place++ {
			lo := rng.Intn(32)
			w := 1 + rng.Intn(8)
			if lo+w > 32 {
				continue
			}
			overlap := false
			for b := lo; b < lo+w; b++ {
				if busy[b] {
					overlap = true
				}
			}
			if overlap && rng.Intn(8) != 0 {
				continue
			}
			fname := fmt.Sprintf("f%d", fn)
			reset := rng.Uint32() & fieldMask(w)
			if rng.Intn(15) == 0 {
				reset = fieldMask(w) + 1 // 偶发复位值越界
			}
			fields = append(fields, FieldDef{
				Name:   fname,
				Lo:     lo,
				Width:  w,
				Access: Access(rng.Intn(6)),
				Reset:  reset,
			})
			if !overlap {
				for b := lo; b < lo+w; b++ {
					busy[b] = true
				}
			}
			fn++
		}
		if intentionallyBad && i == n-1 {
			switch rng.Intn(3) {
			case 0:
				name = ""
			case 1:
				if len(defs) > 0 {
					name = defs[0].Name
				}
			case 2:
				fields = append(fields, FieldDef{Name: "bad", Lo: -1, Width: 2, Access: RW})
			}
		}
		defs = append(defs, RegDef{Name: name, Fields: fields})
	}
	return defs, intentionallyBad
}

func sameErr(a, b error) bool { return errors.Is(a, b) && errors.Is(b, a) }

// TestNaiveCrossCheck 用相同的随机定义与操作序列驱动产品实现与朴素模拟，逐步对照。
func TestNaiveCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for iter := 0; iter < 300; iter++ {
		defs, _ := randomDefs(rng)
		real, errReal := New(defs)
		naive, errNaive := naiveNew(defs)
		if !sameErr(errReal, errNaive) {
			t.Fatalf("iter %d: New 拒绝不一致 real=%v naive=%v", iter, errReal, errNaive)
		}
		if errReal != nil {
			continue
		}
		regName := defs[rng.Intn(len(defs))].Name
		for step := 0; step < 80; step++ {
			desc := ""
			switch rng.Intn(8) {
			case 0:
				real.Reset()
				naive.reset()
				desc = "Reset"
			case 1:
				val := rng.Uint32()
				be := rng.Intn(18) - 1 // -1..16
				desc = fmt.Sprintf("Write(%s,%#08x,be=%d)", regName, val, be)
				e1 := real.Write(regName, val, be)
				e2 := naive.write(regName, val, be)
				if !sameErr(e1, e2) {
					t.Fatalf("iter %d %s err %v vs %v", iter, desc, e1, e2)
				}
			case 2:
				name := regName
				if rng.Intn(10) == 0 {
					name = "GHOST"
				}
				desc = fmt.Sprintf("Read(%s)", name)
				v1, e1 := real.Read(name)
				v2, e2 := naive.read(name)
				if v1 != v2 || !sameErr(e1, e2) {
					t.Fatalf("iter %d %s -> %#x(%v) vs %#x(%v)", iter, desc, v1, e1, v2, e2)
				}
			case 3:
				v1, e1 := real.Raw(regName)
				v2, e2 := naive.raw(regName)
				if v1 != v2 || !sameErr(e1, e2) {
					t.Fatalf("iter %d Raw(%s) %#x vs %#x", iter, regName, v1, v2)
				}
			case 4:
				mask, value := rng.Uint32(), rng.Uint32()
				desc = fmt.Sprintf("RMW(%s,%#08x,%#08x)", regName, mask, value)
				r1, s1, e1 := real.ReadModifyWrite(regName, mask, value)
				r2, s2, e2 := naive.rmw(regName, mask, value)
				if r1 != r2 || s1 != s2 || !sameErr(e1, e2) {
					t.Fatalf("iter %d %s -> (%#x,%#x,%v) vs (%#x,%#x,%v)",
						iter, desc, r1, s1, e1, r2, s2, e2)
				}
			case 5:
				rd := defs[findReg(defs, regName)]
				if len(rd.Fields) == 0 {
					continue
				}
				fname := rd.Fields[rng.Intn(len(rd.Fields))].Name
				v := rng.Uint32()
				if rng.Intn(10) == 0 {
					fname = "GHOST"
				}
				desc = fmt.Sprintf("HwSet(%s,%s,%#x)", regName, fname, v)
				e1 := real.HwSet(regName, fname, v)
				e2 := naive.hwSet(regName, fname, v)
				if !sameErr(e1, e2) {
					t.Fatalf("iter %d %s err %v vs %v", iter, desc, e1, e2)
				}
			}
			for _, d := range defs {
				s1, _ := real.Raw(d.Name)
				s2, _ := naive.raw(d.Name)
				if s1 != s2 {
					t.Fatalf("iter %d step %d after %s: %s 存值分歧 %#08x vs %#08x",
						iter, step, desc, d.Name, s1, s2)
				}
			}
		}
		final, _ := real.Raw(regName)
		t.Logf("iter=%3d input=80 步随机序列 on %s | output=Raw=%#010x | verdict=返回值/错误/存值与逐位朴素模拟完全一致",
			iter, regName, final)
	}
}

func findReg(defs []RegDef, name string) int {
	for i, d := range defs {
		if d.Name == name {
			return i
		}
	}
	return 0
}
