package window

import "errors"

var ErrInvalidCapacity = errors.New("window: capacity must be positive")

type Ring struct {
	data []byte
	next int
	size int
}

func New(capacity int) (*Ring, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Ring{data: make([]byte, capacity)}, nil
}

func (r *Ring) Cap() int { return len(r.data) }

func (r *Ring) Len() int { return r.size }

func (r *Ring) Reset() {
	r.next, r.size = 0, 0
}

func (r *Ring) Add(b byte) {
	r.data[r.next] = b
	r.next++
	if r.next == len(r.data) {
		r.next = 0
	}
	if r.size < len(r.data) {
	r.size++
	}
}

func (r *Ring) AddBytes(p []byte) {
	if len(p) >= len(r.data) {
		copy(r.data, p[len(p)-len(r.data):])
		r.next, r.size = 0, len(r.data)
		return
	}
	for _, b := range p {
		r.Add(b)
	}
}

func (r *Ring) At(distance int) byte {
	if distance <= 0 || distance > r.size {
		panic("window: invalid distance")
	}
	idx := r.next - distance
	if idx < 0 {
		idx += len(r.data)
	}
	return r.data[idx]
}
