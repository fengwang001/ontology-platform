package syncookie

const hashPrime uint32 = 0xd6e8feb9

// testHash is a deterministic, stateless injection used across the tests.
func testHash(caddr, saddr uint32, cport, sport uint16, cisn, t uint32) uint32 {
	x := caddr*0x9e3779b1 ^ saddr*0x85ebca77 ^
		uint32(cport)*0xc2b2ae3d ^ uint32(sport)*0x27d4eb2f ^
		cisn*0x165667b1 ^ t*hashPrime
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return x
}

// newCounterISN returns an injection handing out 1000, 1001, ...
func newCounterISN() func() uint32 {
	n := uint32(1000)
	return func() uint32 {
		v := n
		n++
		return v
	}
}

func mkKey(id uint32) Key {
	return Key{CAddr: 1000 + id, CPort: uint16(40000 + id), SAddr: 2000, SPort: 80}
}
