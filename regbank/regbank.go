// Package regbank 实现一个带访问类型（access type）的 32 位寄存器位域模拟器。
package regbank

import (
	"errors"
	"sync"
)

// Access 描述位域的总线访问语义。
type Access int

const (
	// RW：写入即存值，读返回当前值。
	RW Access = iota
	// RO：写被忽略，读返回当前值，只能由 HwSet/Reset 改变。
	RO
	// WO：写入即存值，读返回 0，Raw 可见存值。
	WO
	// W1C：写 1 清 0，写 0 不变。
	W1C
	// W1S：写 1 置 1，写 0 不变。
	W1S
	// RC：写被忽略，读返回当前值并随后清零该位域。
	RC
)

// FieldDef 是登记时的位域定义。
type FieldDef struct {
	Name   string
	Lo     int
	Width  int
	Access Access
	Reset  uint32
}

// RegDef 是登记时的寄存器定义。
type RegDef struct {
	Name   string
	Fields []FieldDef
}

// 可区分的错误原因。所有布局问题共用 ErrLayout。
var (
	ErrLayout     = errors.New("regbank: invalid register layout")
	ErrNoRegister = errors.New("regbank: register does not exist")
	ErrInvalidBE  = errors.New("regbank: byte enable out of range")
	ErrNoField    = errors.New("regbank: field does not exist")
	ErrValueRange = errors.New("regbank: value out of field range")
)

type field struct {
	name   string
	lo     int
	width  int
	access Access
	reset  uint32
	value  uint32
	mask   uint32 // (1<<width)-1
	byteBE uint8  // 覆盖到的字节使能位
}

type register struct {
	name   string
	fields []*field
	mu     sync.Mutex
}

// Bank 是一组寄存器的并发安全模拟器。
type Bank struct {
	regs map[string]*register
}

// New 校验并登记寄存器，任何布局问题都返回 ErrLayout。
func New(defs []RegDef) (*Bank, error) {
	b := &Bank{regs: map[string]*register{}}
	for _, rd := range defs {
		if rd.Name == "" {
			return nil, ErrLayout
		}
		if _, dup := b.regs[rd.Name]; dup {
			return nil, ErrLayout
		}
		r := &register{name: rd.Name}
		names := map[string]struct{}{}
		var covered uint64 // 位 i 表示 32 位字的第 i 位已被覆盖
		for _, fd := range rd.Fields {
			if fd.Name == "" {
				return nil, ErrLayout
			}
			if _, dup := names[fd.Name]; dup {
				return nil, ErrLayout
			}
			if fd.Lo < 0 || fd.Width < 1 || fd.Lo+fd.Width > 32 {
				return nil, ErrLayout
			}
			var span uint64 = (uint64(1) << uint(fd.Width)) - 1
			bits := span << uint(fd.Lo)
			if covered&bits != 0 {
				return nil, ErrLayout
			}
			var fmask uint32
			if fd.Width == 32 {
				fmask = ^uint32(0)
			} else {
				fmask = uint32(1)<<uint(fd.Width) - 1
			}
			if fd.Reset > fmask {
				return nil, ErrLayout
			}
			names[fd.Name] = struct{}{}
			covered |= bits
			f := &field{
				name:   fd.Name,
				lo:     fd.Lo,
				width:  fd.Width,
				access: fd.Access,
				reset:  fd.Reset,
				value:  fd.Reset,
				mask:   fmask,
			}
			firstByte := fd.Lo / 8
			lastByte := (fd.Lo + fd.Width - 1) / 8
			for i := firstByte; i <= lastByte; i++ {
				f.byteBE |= uint8(1) << uint(i)
			}
			r.fields = append(r.fields, f)
		}
		b.regs[rd.Name] = r
	}
	return b, nil
}

// Reset 把全部位域恢复为复位值。
func (b *Bank) Reset() {
	for _, r := range b.regs {
		r.mu.Lock()
		for _, f := range r.fields {
			f.value = f.reset
		}
		r.mu.Unlock()
	}
}

// Write 按字节使能与各访问类型处理一次总线写。
func (b *Bank) Write(reg string, val uint32, be int) error {
	r, ok := b.regs[reg]
	if !ok {
		return ErrNoRegister
	}
	if be < 0 || be > 15 {
		return ErrInvalidBE
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	writeLocked(r, val, uint8(be))
	return nil
}

// writeLocked 要求调用方持有 r.mu。
func writeLocked(r *register, val uint32, be uint8) {
	for _, f := range r.fields {
		if f.byteBE&^be != 0 {
			continue // 位域覆盖的字节未被全部使能：整体不变
		}
		fv := (val >> uint(f.lo)) & f.mask
		switch f.access {
		case RW, WO:
			f.value = fv
		case W1C:
			f.value &^= fv
		case W1S:
			f.value |= fv
		case RO, RC:
			// 写被忽略
		}
	}
}

// Read 组装返回值，并对 RC 位域读后清零。
func (b *Bank) Read(reg string) (uint32, error) {
	r, ok := b.regs[reg]
	if !ok {
		return 0, ErrNoRegister
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return readLocked(r), nil
}

// readLocked 要求调用方持有 r.mu。
func readLocked(r *register) uint32 {
	var out uint32
	for _, f := range r.fields {
		if f.access == WO {
			continue // WO 读返回 0
		}
		out |= f.value << uint(f.lo)
	}
	for _, f := range r.fields {
		if f.access == RC {
			f.value = 0
		}
	}
	return out
}

// rawLocked 要求调用方持有 r.mu。
func rawLocked(r *register) uint32 {
	var out uint32
	for _, f := range r.fields {
		out |= f.value << uint(f.lo)
	}
	return out
}

// ReadModifyWrite 展开为 Read + Write，返回读值与写后原始存值。
func (b *Bank) ReadModifyWrite(reg string, mask, value uint32) (uint32, uint32, error) {
	r, ok := b.regs[reg]
	if !ok {
		return 0, 0, ErrNoRegister
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	readVal := readLocked(r)
	w := (readVal &^ mask) | (value & mask)
	writeLocked(r, w, 15)
	return readVal, rawLocked(r), nil
}

// HwSet 不受访问类型约束地直接设置位域存值。
func (b *Bank) HwSet(reg, fieldName string, v uint32) error {
	r, ok := b.regs[reg]
	if !ok {
		return ErrNoRegister
	}
	var target *field
	for _, f := range r.fields {
		if f.name == fieldName {
			target = f
			break
		}
	}
	if target == nil {
		return ErrNoField
	}
	if v > target.mask {
		return ErrValueRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target.value = v
	return nil
}

// Raw 返回无副作用的各位域原始存值拼成的字。
func (b *Bank) Raw(reg string) (uint32, error) {
	r, ok := b.regs[reg]
	if !ok {
		return 0, ErrNoRegister
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return rawLocked(r), nil
}
