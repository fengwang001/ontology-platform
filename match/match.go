// Package match finds longest matches using a bounded hash chain.
package match

import (
	"errors"

	"ontology/window"
)

// ErrBadConfig is returned for an invalid chain limit.
var ErrBadConfig = errors.New("match: chain limit must be positive")

// Finder searches matches inside a window over an explicit data slice.
type Finder struct {
	win    *window.Window
	chain  int
	head   map[uint32]int
	links  []int
	seq    int
	probed int
}

// New builds a Finder with the given window and per-position chain limit.
func New(win *window.Window, chainLimit int) (*Finder, error) {
	if chainLimit <= 0 {
		return nil, ErrBadConfig
	}
	return &Finder{
		win:   win,
		chain: chainLimit,
		head:  map[uint32]int{},
		links: nil,
		seq:   -1,
	}, nil
}

// Probed reports how many candidate positions have been examined.
func (f *Finder) Probed() int { return f.probed }

// Seed inserts prefix bytes as a preset dictionary (no output offsets).
func (f *Finder) Seed(prefix []byte) {
	f.win.Add(prefix)
	for i := range prefix {
		f.insert(prefix, i, nil, 0)
	}
}

// Find returns the best match ending inside the window for data at pos.
func (f *Finder) Find(data []byte, pos int) (dist, length int) {
	if pos+3 > len(data) {
		return 0, 0
	}
	h := hash3(data, pos)
	cand := f.head[h] - 1
	limit := f.chain
	best := 0
	for limit > 0 && cand >= 0 {
		f.probed++
		d := f.seq - cand
		if d <= 0 || d > f.win.Cap() {
			break
		}
		l := f.compare(data, pos, d)
		if l > best {
			best, dist = l, d
			if best == f.win.Cap() {
				break
			}
		}
		cand = f.links[cand]
		limit--
	}
	if best < 3 {
		return 0, 0
	}
	return dist, best
}

// Add records one emitted source position.
func (f *Finder) Add(data []byte, pos int) {
	f.win.Add(data[pos : pos+1])
	f.insert(data, pos, nil, 0)
}

func (f *Finder) insert(p []byte, i int, q []byte, qi int) {
	if i+3 > len(p) {
		f.seq++
		f.links = append(f.links, -1)
		return
	}
	h := hash3(p, i)
	f.seq++
	f.links = append(f.links, f.head[h]-1)
	f.head[h] = f.seq + 1
}

func (f *Finder) compare(data []byte, pos, dist int) int {
	maxL := f.win.Cap()
	if rem := len(data) - pos; rem < maxL {
		maxL = rem
}
	n := 0
	for n < maxL {
		var b byte
		if n < dist {
			b = f.win.At(dist - n)
		} else {
			b = data[pos+n-dist]
		}
		if b != data[pos+n] {
			break
		}
		n++
}
	return n
}

func hash3(p []byte, i int) uint32 {
	h := uint32(p[i])<<16 | uint32(p[i+1])<<8 | uint32(p[i+2])
	return h * 0x1E35A7BD
}
