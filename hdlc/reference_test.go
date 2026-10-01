package hdlc

// Naive bit-by-bit reference implementations, transcribed directly from the
// protocol rules. They are deliberately simple and inefficient, and serve as
// an independent oracle for the randomized comparison test.

var naiveFlag = []int{0, 1, 1, 1, 1, 1, 1, 0}

type naiveEncoder struct {
	bits    []int
	started bool
}

func (n *naiveEncoder) frame(payload []byte) {
	if !n.started {
		n.bits = append(n.bits, naiveFlag...)
		n.started = true
	}
	fcs := crc16X25(payload)
	content := make([]byte, 0, len(payload)+2)
	content = append(content, payload...)
	content = append(content, byte(fcs), byte(fcs>>8))
	ones := 0
	for _, b := range content {
		for i := 0; i < 8; i++ {
			bit := int((b >> i) & 1)
			n.bits = append(n.bits, bit)
			if bit == 1 {
				ones++
				if ones == 5 {
					n.bits = append(n.bits, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	n.bits = append(n.bits, naiveFlag...)
}

func (n *naiveEncoder) flush() {
	for len(n.bits)%8 != 0 {
		n.bits = append(n.bits, 1)
	}
	n.started = false
}

type naiveDecoder struct {
	last8    []int
	pending  []int
	ones     int
	inFrame  bool
	frames   [][]byte
	aborts   int
	badLen   int
	tooShort int
	badFCS   int
}

func isFlagBits(last8 []int) bool {
	if len(last8) != 8 {
		return false
	}
	for i, b := range naiveFlag {
		if last8[i] != b {
			return false
		}
	}
	return true
}

func (n *naiveDecoder) feedBit(bit int) {
	if n.inFrame {
		n.pending = append(n.pending, bit)
	}
	n.last8 = append(n.last8, bit)
	if len(n.last8) > 8 {
		n.last8 = n.last8[1:]
	}
	if bit == 1 {
		n.ones++
		if n.ones == 7 {
			n.aborts++
			n.pending = nil
			n.last8 = nil
			n.ones = 0
			n.inFrame = false
			return
		}
	} else {
		n.ones = 0
	}
	if isFlagBits(n.last8) {
		if n.inFrame {
			var content []int
			if len(n.pending) > 8 {
				content = append(content, n.pending[:len(n.pending)-8]...)
			}
			n.handleContent(content)
		}
		n.pending = nil
		n.inFrame = true
	}
}

func (n *naiveDecoder) handleContent(raw []int) {
	if len(raw) == 0 {
		return // idle between flags
	}
	var bits []int
	ones := 0
	for _, b := range raw {
		if b == 1 {
			bits = append(bits, 1)
			ones++
			continue
		}
		if ones == 5 {
			ones = 0 // stuff bit
			continue
		}
		bits = append(bits, 0)
		ones = 0
	}
	if len(bits)%8 != 0 {
		n.badLen++
		return
	}
	if len(bits)/8 < 3 {
		n.tooShort++
		return
	}
	buf := make([]byte, len(bits)/8)
	for i, b := range bits {
		buf[i/8] |= byte(b) << (i % 8)
	}
	payload := buf[:len(buf)-2]
	fcs := uint16(buf[len(buf)-2]) | uint16(buf[len(buf)-1])<<8
	if crc16X25(payload) != fcs {
		n.badFCS++
		return
	}
	n.frames = append(n.frames, append([]byte{}, payload...))
}

func packBitsLSB(bits []int) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		out[i/8] |= byte(b) << (i % 8)
	}
	return out
}

func unpackBitsLSB(data []byte, nbits int) []int {
	bits := make([]int, 0, nbits)
	for i := 0; i < nbits; i++ {
		bits = append(bits, int((data[i/8]>>(i%8))&1))
	}
	return bits
}
