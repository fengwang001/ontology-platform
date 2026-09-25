package hash

const (
	Base = 257
	Mod  = 65521
)

type Window struct {
	base uint64
	mod  uint64
	pow  uint64
	size int
	h    uint64
}

func New(base, mod uint64, size int) *Window {
	pow := uint64(1)
	for range size - 1 {
		pow = pow * base % mod
	}
	return &Window{base: base, mod: mod, pow: pow, size: size}
}

func (w *Window) Append(c byte) {
	w.h = (w.h*w.base + uint64(c)) % w.mod
}

func (w *Window) Remove(old, next byte) {
	w.h = (w.h + w.mod - uint64(old)*w.pow%w.mod) % w.mod
	w.h = (w.h*w.base + uint64(next)) % w.mod
}

func (w *Window) Value() uint64 {
	return w.h
}
