package window

import "fmt"

type Ring struct {
	buf   []byte
	head  int
	size  int
	total int
}

func New(capacity int) *Ring {
	if capacity <= 0 {
		panic(fmt.Sprintf("window: capacity must be positive, got %d", capacity))
	}
	return &Ring{buf: make([]byte, capacity)}
}

func (r *Ring) Cap() int   { return len(r.buf) }
func (r *Ring) Len() int   { return r.size }
func (r *Ring) Total() int { return r.total }

func (r *Ring) Reset() {
	r.head, r.size, r.total = 0, 0, 0
}

func (r *Ring) Seed(p []byte) {
	r.Reset()
	copy(r.buf, p)
	r.head, r.size, r.total = len(p)%len(r.buf), len(p), len(p)
}

func (r *Ring) SeedTail(prefix []byte, fullLength int) {
	if fullLength < len(prefix) || len(prefix) > len(r.buf) {
		panic("window: invalid seed")
	}
	r.Seed(prefix)
	r.total = fullLength
}

func (r *Ring) AddByte(b byte) {
	r.buf[r.head] = b
	r.head++
	if r.head == len(r.buf) {
		r.head = 0
	}
	if r.size < len(r.buf) {
		r.size++
	}
	r.total++
}

func (r *Ring) At(distance int) byte {
	if distance <= 0 || distance > r.size {
		panic(fmt.Sprintf("window: invalid distance %d (size %d)", distance, r.size))
	}
	idx := r.head - distance
	if idx < 0 {
		idx += len(r.buf)
	}
	return r.buf[idx]
}

func (r *Ring) ByteAt(position int) byte {
	if position < 0 || r.total-position > len(r.buf) || position >= r.total {
		panic(fmt.Sprintf("window: invalid position %d (total %d)", position, r.total))
	}
	return r.buf[position%len(r.buf)]
}
