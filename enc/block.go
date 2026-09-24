package enc

import (
	"ontology/match"
	"ontology/window"
	"ontology/wire"
)

// block is a deterministic LZ77 record emitter. Decisions depend only on the
// accumulated byte sequence, so feeding bytes one at a time (streaming) or in
// bulk yields identical records.
type block struct {
	win  *window.Window
	m    *match.Matcher
	out  []byte
	lit  []byte
	next int // first not-yet-decided absolute position
	minL int
}

func newBlock(winSize, chain, minL int) *block {
	win := window.New(winSize)
	return &block{win: win, m: match.New(win, chain), minL: minL}
}

// seed fills window/hash chains with a preset dictionary (previous block tail).
func (b *block) seed(dict []byte) {
	for _, c := range dict {
		p := b.win.Len()
		b.win.Add(c)
		b.m.Insert(p)
		b.next = p + 1
	}
}

func (b *block) emitLit() {
	if len(b.lit) > 0 {
		b.out = wire.AppendLit(b.out, b.lit)
		b.lit = b.lit[:0]
	}
}

// decide emits one token at pos. If force is false and the match extends to
// the newest available byte (pos+l==end), its true length is still unknown,
// so it returns false and the position waits for more input.
func (b *block) decide(pos, end int, force bool) bool {
	avail := end - pos
	if avail < 3 {
		if !force {
			return false
		}
		b.lit = append(b.lit, b.win.At(pos))
		b.m.Insert(pos)
		b.next = pos + 1
		return true
	}
	d, l := b.m.Find(pos, avail)
	if l >= b.minL && (force || pos+l < end) {
		b.emitLit()
		b.out = wire.AppendMatch(b.out, d, l)
		for k := 0; k < l; k++ {
			b.m.Insert(pos + k)
		}
		b.next = pos + l
		return true
	}
	if l < b.minL { // mismatch byte already seen: literal is final
		b.lit = append(b.lit, b.win.At(pos))
		b.m.Insert(pos)
		b.next = pos + 1
		return true
	}
	return false // match reaches newest byte; wait
}

// add pushes one byte and decides every position whose token end is known.
func (b *block) add(c byte) {
	b.win.Add(c)
	end := b.win.Len()
	for b.next < end {
		if !b.decide(b.next, end, false) {
			break
		}
	}
}

// drain forces every buffered byte to be decided (flush / EOF / block edge).
func (b *block) drain(flushTag bool) {
	end := b.win.Len()
	for b.next < end {
		b.decide(b.next, end, true)
	}
	b.emitLit()
	if flushTag {
		b.out = wire.AppendFlush(b.out)
	}
}

// run compresses all data, deciding every byte (no flush tag emitted).
func (b *block) run(data []byte) []byte {
	for _, c := range data {
		b.add(c)
	}
	b.drain(false)
	return b.out
}
