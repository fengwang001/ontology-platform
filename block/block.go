package block

import (
	"hash/crc32"
	"sync/atomic"
)

// Stats holds observable work counters for lookup/scan tests.
type Stats struct {
	DecodeSteps int64 // entries decoded from a restart anchor
	BlockReads  int64 // blocks whose entry data got decoded
}

func (s *Stats) addDecodes(n int) { atomic.AddInt64(&s.DecodeSteps, int64(n)) }
func (s *Stats) addBlock()        { atomic.AddInt64(&s.BlockReads, 1) }

// Block is an immutable decoded prefix-compressed block.
type Block struct {
	raw       []byte
	h         header
	data      []byte
	values    []string // full decode, used by At
	restartAt []uint32
	indexOK   bool
	stats     *Stats
}

// Parse validates and decodes one block.
func Parse(raw []byte) (*Block, error) {
	h, err := parseHeader(raw)
	if err != nil {
		return nil, err
	}
	dataEnd := hdrLen + h.dataLen
	restartEnd := dataEnd + h.restartCount*4
	totalEnd := restartEnd + 4
	if uint64(len(raw)) < uint64(dataEnd) {
		return nil, ErrEntry
	}
	if uint64(len(raw)) < uint64(restartEnd) {
		return nil, ErrRestart
	}
	if uint64(len(raw)) < uint64(totalEnd) {
		return nil, ErrCRC
	}
	if u32(raw[restartEnd:totalEnd]) != crc32.ChecksumIEEE(raw[:restartEnd]) {
		return nil, ErrCRC
	}
	b := &Block{raw: raw, h: h, data: raw[hdrLen:dataEnd], indexOK: true, stats: &Stats{}}
	b.restartAt = make([]uint32, h.restartCount)
	for i := range b.restartAt {
		b.restartAt[i] = u32(raw[uint64(dataEnd)+uint64(i*4):])
	}
	values, offs, err := decodeAll(b.data)
	if err != nil || len(values) != int(h.n) || len(offs) != len(b.restartAt) {
		return nil, ErrEntry
	}
	for i, want := range b.restartAt {
		if want != offs[i] {
			b.indexOK = false // bad restart table: keep block, fallback to head scan
		}
	}
	b.values = values
	return b, nil
}

func decodeAll(data []byte) (vals []string, restartOff []uint32, err error) {
	prev := ""
	pos := 0
	for pos < len(data) {
		if pos+8 > len(data) {
			return nil, nil, ErrEntry
		}
		field, diffLen := u32(data[pos:]), int(u32(data[pos+4:]))
		shared := int(field &^ restartFlag)
		pos += 8
		if shared > len(prev) || pos+diffLen > len(data) {
			if shared > len(prev) {
				return nil, nil, ErrSharedLen
			}
			return nil, nil, ErrEntry
		}
		if field&restartFlag != 0 {
			restartOff = append(restartOff, uint32(pos-8))
		}
		v := prev[:shared] + string(data[pos:pos+diffLen])
		pos += diffLen
		vals = append(vals, v)
		prev = v
	}
	return vals, restartOff, nil
}

func (b *Block) Len() int          { return int(b.h.n) }
func (b *Block) K() int            { return int(b.h.k) }
func (b *Block) RestartCount() int { return int(b.h.restartCount) }
func (b *Block) IndexOK() bool     { return b.indexOK }
func (b *Block) Stats() *Stats     { return b.stats }

// At returns the value by index using the full in-memory decode.
func (b *Block) At(i int) string { return b.values[i] }

// RestartValue decodes only restart r's anchor entry (shared==0).
func (b *Block) RestartValue(r int) (string, error) {
	off := int(b.restartAt[r])
	if off+8 > len(b.data) {
		return "", ErrRestartOffset
	}
	field, diffLen := u32(b.data[off:]), int(u32(b.data[off+4:]))
	if field&restartFlag == 0 || off+8+diffLen > len(b.data) {
		return "", ErrRestartOffset
	}
	return string(b.data[off+8 : off+8+diffLen]), nil
}

// Iter sequentially decodes entries starting at restart r's interval.
type Iter struct {
	b     *Block
	pos   int
	idx   int
	prev  string
	steps int
	limit int // stop index (exclusive)
	err   error
}

// Iter returns an iterator over the interval starting at restart r.
// If the restart index is invalid it falls back to scanning from head.
func (b *Block) Iter(r int) (*Iter, error) {
	off := 0
	idx := 0
	if b.indexOK {
		off = int(b.restartAt[r])
		idx = r * b.K()
	}
	if off < 0 || off >= len(b.data) {
		return nil, ErrRestartOffset
	}
	limit := b.Len()
	if r+1 < b.RestartCount() {
		limit = (r + 1) * b.K()
	}
	if !b.indexOK {
		limit = b.Len()
	}
	b.stats.addBlock()
	return &Iter{b: b, pos: off, idx: idx, limit: limit}, nil
}

// Next yields the next entry; ok=false at interval end or corruption.
func (it *Iter) Next() (idx int, val string, ok bool) {
	if it.err != nil || it.idx >= it.limit || it.pos >= len(it.b.data) {
		return 0, "", false
	}
	if it.pos+8 > len(it.b.data) {
		it.err = ErrEntry
		return 0, "", false
	}
	field, diffLen := u32(it.b.data[it.pos:]), int(u32(it.b.data[it.pos+4:]))
	shared := int(field &^ restartFlag)
	it.pos += 8
	if shared > len(it.prev) {
		it.err = ErrSharedLen
		return 0, "", false
	}
	if it.pos+diffLen > len(it.b.data) {
		it.err = ErrEntry
		return 0, "", false
	}
	val = it.prev[:shared] + string(it.b.data[it.pos:it.pos+diffLen])
	it.pos += diffLen
	it.steps++
	it.b.stats.addDecodes(1)
	idx, it.idx = it.idx, it.idx+1
	it.prev = val
	return idx, val, true
}

// Err returns corruption discovered during iteration.
func (it *Iter) Err() error { return it.err }

// Recover extracts the maximal strictly-ordered prefix from a truncated block.
func Recover(raw []byte) ([]string, error) {
	h, err := parseHeader(raw)
	if err != nil {
		return nil, err
	}
	end := hdrLen + h.dataLen
	if int(end) > len(raw) {
		end = uint32(len(raw))
	}
	data := raw[hdrLen:end]
	out := make([]string, 0)
	prev := ""
	pos := 0
	for pos < len(data) {
		if pos+8 > len(data) {
			break
		}
		field, diffLen := u32(data[pos:]), int(u32(data[pos+4:]))
		shared := int(field &^ restartFlag)
		pos += 8
		if shared > len(prev) || pos+diffLen > len(data) {
			break
		}
		v := prev[:shared] + string(data[pos:pos+diffLen])
		pos += diffLen
		if len(out) > 0 && v <= prev {
			break // first disordered/duplicate entry: stop, prefix stays strict
		}
		out = append(out, v)
		prev = v
	}
	_ = h
	return out, nil
}
