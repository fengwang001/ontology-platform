package regbank

// ---- Naive bit-by-bit reference model ----

type naiveBank struct {
	stored  map[string]map[string]uint32 // reg -> field -> stored value
	fieldOf map[string]map[string]FieldSpec
}

func newNaive(specs []RegSpec) *naiveBank {
	n := &naiveBank{
		stored:  map[string]map[string]uint32{},
		fieldOf: map[string]map[string]FieldSpec{},
	}
	for _, rs := range specs {
		n.stored[rs.Name] = map[string]uint32{}
		n.fieldOf[rs.Name] = map[string]FieldSpec{}
		for _, fs := range rs.Fields {
			n.stored[rs.Name][fs.Name] = fs.Reset
			n.fieldOf[rs.Name][fs.Name] = fs
		}
	}
	return n
}

func (n *naiveBank) getBit(reg string, bit int) (bool, FieldSpec) {
	for _, fs := range n.fieldOf[reg] {
		if bit >= fs.Lo && bit < fs.Lo+fs.W {
			return n.stored[reg][fs.Name]&(1<<uint(bit-fs.Lo)) != 0, fs
		}
	}
	return false, FieldSpec{}
}

func (n *naiveBank) raw(reg string) uint32 {
	var out uint32
	for bit := 0; bit < 32; bit++ {
		v, _ := n.getBit(reg, bit)
		if v {
			out |= 1 << uint(bit)
		}
	}
	return out
}

func (n *naiveBank) write(reg string, val uint32, be int) {
	for _, fs := range n.fieldOf[reg] {
		enabled := true
		for b := fs.Lo; b < fs.Lo+fs.W; b++ {
			if be&(1<<uint(b/8)) == 0 {
				enabled = false
				break
			}
		}
		if !enabled {
			continue
		}
		cur := n.stored[reg][fs.Name]
		mask := (uint32(1) << uint(fs.W)) - 1
		switch fs.Access {
		case RW, WO:
			cur = (val >> uint(fs.Lo)) & mask
		case W1C:
			for b := 0; b < fs.W; b++ {
				if val&(1<<uint(fs.Lo+b)) != 0 {
					cur &^= 1 << uint(b)
				}
			}
		case W1S:
			for b := 0; b < fs.W; b++ {
				if val&(1<<uint(fs.Lo+b)) != 0 {
					cur |= 1 << uint(b)
				}
			}
		case RO, RC:
			// ignored
		}
		n.stored[reg][fs.Name] = cur
	}
}

func (n *naiveBank) read(reg string) uint32 {
	var out uint32
	for bit := 0; bit < 32; bit++ {
		v, fs := n.getBit(reg, bit)
		if fs.Access == WO {
			continue
		}
		if v {
			out |= 1 << uint(bit)
		}
	}
	for name, fs := range n.fieldOf[reg] {
		if fs.Access == RC {
			n.stored[reg][name] = 0
		}
	}
	return out
}

func (n *naiveBank) rmw(reg string, mask, value uint32) (uint32, uint32) {
	r := n.read(reg)
	w := (r &^ mask) | (value & mask)
	n.write(reg, w, 0xf)
	return r, n.raw(reg)
}

func (n *naiveBank) hwSet(reg, name string, v uint32) {
	n.stored[reg][name] = v
}

func (n *naiveBank) reset() {
	for reg, m := range n.fieldOf {
		for name, fs := range m {
			n.stored[reg][name] = fs.Reset
		}
	}
}

// ---- Test layout ----

func validSpecs() []RegSpec {
	return []RegSpec{
		{Name: "MIX", Fields: []FieldSpec{
			{Name: "rw0", Lo: 0, W: 8, Access: RW, Reset: 0x12},
			{Name: "ro0", Lo: 8, W: 8, Access: RO, Reset: 0xAB},
			{Name: "woc", Lo: 16, W: 8, Access: WO, Reset: 0x34},
			{Name: "w1c", Lo: 24, W: 8, Access: W1C, Reset: 0xF0},
		}},
		{Name: "W1SRC", Fields: []FieldSpec{
			{Name: "ws", Lo: 0, W: 12, Access: W1S, Reset: 0x001},
			{Name: "rc", Lo: 16, W: 9, Access: RC, Reset: 0x1FF},
			{Name: "gap", Lo: 30, W: 2, Access: RW, Reset: 0x2},
		}},
		{Name: "STRADDLE", Fields: []FieldSpec{
			{Name: "lo", Lo: 0, W: 4, Access: RW, Reset: 0x1},
			{Name: "cross", Lo: 4, W: 8, Access: RW, Reset: 0x22}, // bits 4..11, spans bytes 0 and 1
			{Name: "mid", Lo: 12, W: 8, Access: W1C, Reset: 0x5A}, // spans bytes 1 and 2
			{Name: "hi", Lo: 24, W: 8, Access: RO, Reset: 0x77},
		}},
	}
}
