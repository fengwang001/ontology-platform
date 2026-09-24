package pipeline

// outBuf is a queue of encoded output pieces with a front offset, so
// confirming bytes never requires copying or rescanning the whole buffer.
type outBuf struct {
	pieces [][]byte
	off    int   // confirmed prefix of pieces[0]
	total  int64 // unconfirmed bytes
}

func (b *outBuf) push(p []byte) {
	if len(p) == 0 {
		return
	}
	b.pieces = append(b.pieces, p)
	b.total += int64(len(p))
}

// front returns the unconfirmed remainder of the head piece.
func (b *outBuf) front() []byte {
	if len(b.pieces) == 0 {
		return nil
	}
	return b.pieces[0][b.off:]
}

// confirm drops n bytes from the front; n must not exceed the front piece.
func (b *outBuf) confirm(n int) {
	b.off += n
	b.total -= int64(n)
	if b.off == len(b.pieces[0]) {
		b.pieces[0] = nil
		b.pieces = b.pieces[1:]
		b.off = 0
	}
}

func (b *outBuf) len() int64 { return b.total }

// snapshot copies the remaining unconfirmed bytes as fresh pieces.
func (b *outBuf) snapshot() [][]byte {
	var out [][]byte
	for i, p := range b.pieces {
		s := p
		if i == 0 {
			s = p[b.off:]
		}
		cp := make([]byte, len(s))
		copy(cp, s)
		out = append(out, cp)
	}
	return out
}
