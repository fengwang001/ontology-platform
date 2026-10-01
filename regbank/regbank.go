package regbank

type AccessType string

const (
	RW  AccessType = "RW"
	RO  AccessType = "RO"
	WO  AccessType = "WO"
	W1C AccessType = "W1C"
	W1S AccessType = "W1S"
	RC  AccessType = "RC"
)

type FieldSpec struct {
	Name   string
	Lo     int
	W      int
	Access AccessType
	Reset  uint32
}

type RegSpec struct {
	Name   string
	Fields []FieldSpec
}

type Bank struct {
	mu   chan struct{}
	regs map[string]*register
}

type register struct {
	spec   RegSpec
	fields []*field
}

type field struct {
	spec  FieldSpec
	value uint32
}

type RMWResult struct {
	Read   uint32
	Stored uint32
}

var validAccess = map[AccessType]bool{
	RW:  true,
	RO:  true,
	WO:  true,
	W1C: true,
	W1S: true,
	RC:  true,
}

// NewBank registers a set of 32-bit registers and validates the whole layout
// before anything is stored. Any layout violation yields ErrLayout.
func NewBank(specs []RegSpec) (*Bank, error) {
	b := &Bank{
		mu:   make(chan struct{}, 1),
		regs: make(map[string]*register, len(specs)),
	}

	seenReg := make(map[string]bool, len(specs))
	for _, rs := range specs {
		if rs.Name == "" || seenReg[rs.Name] {
			return nil, ErrLayout
		}
		seenReg[rs.Name] = true

		r := &register{spec: rs}
		seenField := make(map[string]bool, len(rs.Fields))
		var covered [32]bool
		for _, fs := range rs.Fields {
			if fs.Name == "" || seenField[fs.Name] {
				return nil, ErrLayout
			}
			seenField[fs.Name] = true
			if fs.Lo < 0 || fs.W < 1 || fs.Lo+fs.W > 32 {
				return nil, ErrLayout
			}
			if !validAccess[fs.Access] {
				return nil, ErrLayout
			}
			if fs.Reset >= uint32(1)<<uint(fs.W) {
				return nil, ErrLayout
			}
			for bit := fs.Lo; bit < fs.Lo+fs.W; bit++ {
				if covered[bit] {
					return nil, ErrLayout
				}
				covered[bit] = true
			}
			r.fields = append(r.fields, &field{spec: fs, value: fs.Reset})
		}
		b.regs[rs.Name] = r
	}
	return b, nil
}

// Write applies val to register reg under the 4-bit byte enable be.
// A field is updated only when every byte it touches is enabled.
func (b *Bank) Write(reg string, val uint32, be int) error {
	r, err := b.prepareWrite(reg, be)
	if err != nil {
		return err
	}
	defer b.unlock()
	for _, f := range r.fields {
		if !fieldEnabled(f.spec, be) {
			continue
		}
		fv := extract(val, f.spec.Lo, f.spec.W)
		switch f.spec.Access {
		case RW, WO:
			f.value = fv
		case W1C:
			f.value &^= fv
		case W1S:
			f.value |= fv
		case RO, RC:
			// writes are ignored
		}
	}
	return nil
}

// Read returns the value assembled per field semantics, then clears every RC
// field in the register.
func (b *Bank) Read(reg string) (uint32, error) {
	r, err := b.prepareRead(reg)
	if err != nil {
		return 0, err
	}
	defer b.unlock()
	out := r.readVisible()
	r.clearRC()
	return out, nil
}

// ReadModifyWrite performs Read; w = (r &^ mask) | (value & mask); Write with
// be=15; and reports the read value together with the raw stored word.
func (b *Bank) ReadModifyWrite(reg string, mask, value uint32) (RMWResult, error) {
	r, err := b.prepareRead(reg)
	if err != nil {
		return RMWResult{}, err
	}
	defer b.unlock()

	read := r.readVisible()
	r.clearRC()

	w := (read &^ mask) | (value & mask)
	for _, f := range r.fields {
		fv := extract(w, f.spec.Lo, f.spec.W)
		switch f.spec.Access {
		case RW, WO:
			f.value = fv
		case W1C:
			f.value &^= fv
		case W1S:
			f.value |= fv
		case RO, RC:
			// writes are ignored
		}
	}
	return RMWResult{Read: read, Stored: r.assemble()}, nil
}

// HwSet directly sets a field's stored value from the hardware side.
func (b *Bank) HwSet(reg, fieldName string, v uint32) error {
	r, err := b.prepareField(reg, fieldName)
	if err != nil {
		return err
	}
	defer b.unlock()
	f, err := r.lookupField(fieldName)
	if err != nil {
		return err
	}
	if v >= uint32(1)<<uint(f.spec.W) {
		return ErrValueRange
	}
	f.value = v
	return nil
}

// Raw returns the stored values of all fields without read side effects
// (WO fields are visible, RC fields are not cleared).
func (b *Bank) Raw(reg string) (uint32, error) {
	r, err := b.prepareRead(reg)
	if err != nil {
		return 0, err
	}
	defer b.unlock()
	return r.assemble(), nil
}

// Reset restores every field to its reset value.
func (b *Bank) Reset() {
	b.lock()
	defer b.unlock()
	for _, r := range b.regs {
		for _, f := range r.fields {
			f.value = f.spec.Reset
		}
	}
}

// prepareWrite validates register existence first, then be, and locks the bank
// only after the rejection checks that must not depend on the lock.
func (b *Bank) prepareWrite(reg string, be int) (*register, error) {
	r, ok := b.regs[reg]
	if !ok {
		return nil, ErrUnknownReg
	}
	if be < 0 || be > 0xf {
		return nil, ErrBadBE
	}
	b.lock()
	return r, nil
}

func (b *Bank) prepareRead(reg string) (*register, error) {
	r, ok := b.regs[reg]
	if !ok {
		return nil, ErrUnknownReg
	}
	b.lock()
	return r, nil
}

// prepareField enforces the rejection order: register, be (n/a here), field,
// value. Existence checks happen before taking the lock.
func (b *Bank) prepareField(reg, fieldName string) (*register, error) {
	r, ok := b.regs[reg]
	if !ok {
		return nil, ErrUnknownReg
	}
	if _, err := r.lookupField(fieldName); err != nil {
		return nil, err
	}
	b.lock()
	return r, nil
}

func (b *Bank) lock()   { b.mu <- struct{}{} }
func (b *Bank) unlock() { <-b.mu }

func (r *register) lookupField(name string) (*field, error) {
	for _, f := range r.fields {
		if f.spec.Name == name {
			return f, nil
		}
	}
	return nil, ErrUnknownFld
}

func (r *register) assemble() uint32 {
	var out uint32
	for _, f := range r.fields {
		out |= f.value << uint(f.spec.Lo)
	}
	return out
}

// readVisible assembles the bus read value: WO fields read as 0.
func (r *register) readVisible() uint32 {
	var out uint32
	for _, f := range r.fields {
		if f.spec.Access == WO {
			continue
		}
		out |= f.value << uint(f.spec.Lo)
	}
	return out
}

func (r *register) clearRC() {
	for _, f := range r.fields {
		if f.spec.Access == RC {
			f.value = 0
		}
	}
}

func extract(word uint32, lo, w int) uint32 {
	return (word >> uint(lo)) & ((uint32(1) << uint(w)) - 1)
}

// fieldEnabled reports whether every byte overlapped by the field is enabled
// in be. Bits [lo, lo+w) map to byte indices lo/8 .. (lo+w-1)/8.
func fieldEnabled(fs FieldSpec, be int) bool {
	first := fs.Lo / 8
	last := (fs.Lo + fs.W - 1) / 8
	for i := first; i <= last; i++ {
		if be&(1<<uint(i)) == 0 {
			return false
		}
	}
	return true
}
