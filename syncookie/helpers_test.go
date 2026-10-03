package syncookie

// testHash is a deterministic FNV-style mix used by all tests.
func testHash(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32 {
	h := uint32(2166136261)
	for _, x := range []uint32{caddr, saddr, uint32(cport)<<16 | uint32(sport), cisn, t} {
		h ^= x
		h *= 16777619
	}
	return h
}

// isnGen returns a deterministic ISN generator: start, start+7, start+14, ...
func isnGen(start uint32) func() uint32 {
	next := start
	return func() uint32 {
		v := next
		next += 7
		return v
	}
}

func k1() Key { return Key{CAddr: 0x0A000001, CPort: 40001, SAddr: 0x0A0000FE, SPort: 80} }
func k2() Key { return Key{CAddr: 0x0A000002, CPort: 40002, SAddr: 0x0A0000FE, SPort: 80} }
func k3() Key { return Key{CAddr: 0x0A000003, CPort: 40003, SAddr: 0x0A0000FE, SPort: 80} }
func k4() Key { return Key{CAddr: 0x0A000004, CPort: 40004, SAddr: 0x0A0000FE, SPort: 80} }
