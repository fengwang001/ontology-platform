// Package match finds longest LZ77 matches with a capped hash chain.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match worth encoding.
const MinMatch = 3

// Config configures a Finder.
type Config struct {
	WindowCap  int // ring capacity in bytes; must be > 0
	ChainLimit int // max candidate positions examined per input position; must be > 0
	NiceLen    int // stop searching once a match this long is found; 0 => 128
}

// Token is one literal run or one back-reference.
type Token struct {
	Lit      []byte // when Len == 0: literal bytes
	Distance int
	Length   int
}

// Finder is a greedy hash-chain matcher. A Finder is not safe for
// concurrent use.
type Finder struct {
	cfg    Config
	win    *window.Window
	head   []int64 // hash bucket -> newest absolute position + 1
	chain  []int64 // ring: older candidate for each absolute position + 1
	probes int64
}

const tableBits = 16

// New validates cfg and builds a Finder.
func New(cfg Config) (*Finder, error) {
	if cfg.WindowCap <= 0 {
		return nil, errors.New("match: WindowCap must be positive")
	}
	if cfg.ChainLimit <= 0 {
		return nil, errors.New("match: ChainLimit must be positive")
}
	if cfg.NiceLen <= 0 {
		cfg.NiceLen = 128
	}
	return &Finder{
		cfg:   cfg,
	win:   window.New(cfg.WindowCap),
	head:  make([]int64, 1<<tableBits),
		chain: make([]int64, cfg.WindowCap),
	}, nil
}

// Window exposes the backing window (used for preset dictionaries).
func (f *Finder) Window() *window.Window { return f.win }

// Probes reports how many candidate positions have been examined.
func (f *Finder) Probes() int64 { return f.probes }

// ResetProbes zeroes the candidate counter.
func (f *Finder) ResetProbes() { f.probes = 0 }

func hash3(b []byte) uint32 {
	return (uint32(b[0])<<10 ^ uint32(b[1])<<5 ^ uint32(b[2])) & (1<<tableBits - 1)
}

func (f *Finder) insert(pos int64, b []byte) {
	h := hash3(b)
	old := f.head[h]
	f.head[h] = pos + 1
	f.chain[pos%int64(f.cfg.WindowCap)] = old
}

// prevAt returns the historical byte at absolute position cp+k, or the
// already-decoded overlap byte from src when the match extends past pos.
func (f *Finder) prevAt(cp, k, dist int64, src []byte, off int) byte {
	if k < dist {
		return f.win.AtAbs(cp + k)
	}
	return src[off+int(k-dist)]
}

// Encode runs greedy matching over src. preset bytes prefill history but
// emit no tokens. Every Finder invocation is deterministic in its inputs.
func (f *Finder) Encode(preset, src []byte) []Token {
	f.win.PutSeq(preset)
	for i := 0; i < len(preset)-2; i++ {
		f.insert(int64(i), preset[i:])
	}
	var toks []Token
	var lit []byte
	flushLit := func() {
		if len(lit) > 0 {
			toks = append(toks, Token{Lit: lit})
			lit = nil
		}
	}
	for off := 0; off < len(src); {
		pos := f.win.Total()
		if off+2 < len(src) {
			f.insert(pos, src[off:])
		}
		bestLen, bestDist := 0, 0
		if off+2 < len(src) {
			h := hash3(src[off:])
			cp := f.head[h] - 1
			if cp == pos {
				cp = f.chain[cp%int64(f.cfg.WindowCap)] - 1
			}
			for c := 0; c < f.cfg.ChainLimit && cp >= 0 && pos-cp <= int64(f.cfg.WindowCap); c++ {
				f.probes++
				dist := pos - cp
				limit := len(src) - off
				k := bestLen // no point rechecking a prefix already beaten
				for k < limit && f.prevAt(cp, int64(k), dist, src, off) == src[off+k] {
					k++
				}
				if k > bestLen {
					bestLen, bestDist = k, int(dist)
					if bestLen >= f.cfg.NiceLen {
						break
					}
				}
				cp = f.chain[cp%int64(f.cfg.WindowCap)] - 1
			}
		}
		if bestLen >= MinMatch {
			flushLit()
			toks = append(toks, Token{Distance: bestDist, Length: bestLen})
			for k := 1; k < bestLen; k++ { // skipped positions join the chains
				if off+k+2 < len(src) {
					f.insert(pos+int64(k), src[off+k:])
				}
			}
			f.win.PutSeq(src[off : off+bestLen])
			off += bestLen
			continue
		}
		lit = append(lit, src[off])
		f.win.Put(src[off])
		off++
	}
	flushLit()
	return toks
}
