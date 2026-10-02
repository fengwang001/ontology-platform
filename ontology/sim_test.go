package ontology

import (
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"math/rand"
	"testing"
)

type naiveState struct {
	dict     map[uint32]int
	dictVals []uint32
	fallback bool
	miss     int
}

func newNaive() *naiveState {
	return &naiveState{dict: make(map[uint32]int)}
}

// naiveHybrid is an independently written reference following the run split
// rules line by line.
func naiveHybrid(idx []int, w int) []byte {
	putVar := func(v uint64) []byte {
		b := make([]byte, binary.MaxVarintLen64)
		n := binary.PutUvarint(b, v)
		return b[:n]
	}
	var out []byte
	var p []int
	flushP := func() {
		if len(p) == 0 {
			return
		}
		g := (len(p) + 7) / 8
		out = append(out, putVar(uint64(g<<1|1))...)
		body := make([]byte, g*w)
		for i, v := range p {
			for b := 0; b < w; b++ {
				if v&(1<<uint(b)) != 0 {
					body[(i*w+b)/8] |= 1 << uint((i*w+b)%8)
				}
			}
		}
		out = append(out, body...)
		p = p[:0]
	}

	i := 0
	for i < len(idx) {
		x := idx[i]
		j := i
		for j < len(idx) && idx[j] == x {
			j++
		}
		length := j - i
		if length >= 8 {
			f := (8 - len(p)%8) % 8
			if length-f >= 8 {
				p = append(p, idx[i:i+f]...)
				flushP()
				out = append(out, putVar(uint64((length-f)<<1))...)
				for b := 0; b < (w+7)/8; b++ {
					out = append(out, byte(x>>(8*b)))
				}
			} else {
				p = append(p, idx[i:j]...)
			}
		} else {
			p = append(p, idx[i:j]...)
		}
		i = j
	}
	flushP()
	return out
}

func (s *naiveState) flush(values []uint32, maxDict int) (Page, string) {
	rows := len(values)
	plainData := make([]byte, 4*rows)
	for i, v := range values {
		binary.LittleEndian.PutUint32(plainData[4*i:], v)
	}
	plainPage := Page{Enc: EncPlain, Rows: rows, Data: plainData}

	if s.fallback {
		return plainPage, "sticky-fallback"
	}

	merged := make(map[uint32]int, len(s.dict))
	for k, v := range s.dict {
		merged[k] = v
	}
	var newVals []uint32
	idxs := make([]int, rows)
	for i, v := range values {
		d, ok := merged[v]
		if !ok {
			d = len(s.dictVals) + len(newVals)
			merged[v] = d
			newVals = append(newVals, v)
		}
		idxs[i] = d
	}
	distinct := len(s.dictVals) + len(newVals)

	if distinct > maxDict {
		s.fallback = true
		return plainPage, "dict-overflow"
	}

	w := 0
	if distinct > 1 {
		w = bits.Len(uint(distinct - 1))
	}
	h := naiveHybrid(idxs, w)
	dictSize := 1 + len(h) + 4*len(newVals)
	plainSize := 4 * rows

	if dictSize < plainSize {
		for _, v := range newVals {
			s.dict[v] = len(s.dictVals)
			s.dictVals = append(s.dictVals, v)
		}
		s.miss = 0
		data := make([]byte, 1+len(h))
		data[0] = byte(w)
		copy(data[1:], h)
		return Page{
			Enc: EncDict, Rows: rows, Width: w,
			DictLen: len(s.dictVals), Data: data,
		}, "dict-wins"
	}

	s.miss++
	if s.miss == 3 {
		s.fallback = true
	}
	return plainPage, "size-loses"
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	const cases = 2000

	for c := 0; c < cases; c++ {
		maxDict := 1 + rng.Intn(24)
		maxRows := 1 + rng.Intn(40)
		alphabet := 1 + rng.Intn(maxDict+8)
		pages := 1 + rng.Intn(6)

		w, err := New(maxDict, maxRows)
		if err != nil {
			t.Fatalf("case %d: %v", c, err)
		}
		ref := newNaive()
		replay, _ := New(maxDict, maxRows)

		for pg := 0; pg < pages; pg++ {
			rows := 1 + rng.Intn(maxRows)
			values := make([]uint32, rows)
			for i := range values {
				// Bias toward repeated values to trigger RLE borrowing.
				if rng.Intn(3) == 0 {
					values[i] = values[max(0, i-1)]
				} else {
					values[i] = uint32(rng.Intn(alphabet))
				}
			}
			for _, v := range values {
				if err := w.Append(v); err != nil {
					t.Fatalf("case %d page %d: append %v", c, pg, err)
				}
				if err := replay.Append(v); err != nil {
					t.Fatalf("replay append: %v", err)
				}
			}

			got, gerr := w.Flush()
			rep, rerr := replay.Flush()
			want, reason := ref.flush(values, maxDict)
			if gerr != nil || rerr != nil {
				t.Fatalf("case %d page %d: flush err %v/%v", c, pg, gerr, rerr)
			}

			t.Logf("case=%d page=%d maxDict=%d maxRows=%d in=%v reason=%s enc=%d width=%d dictLen=%d data=%s plainSize=%d",
				c, pg, maxDict, maxRows, values, reason,
				got.Enc, got.Width, got.DictLen,
				hex.EncodeToString(got.Data), 4*rows)

			if got.Enc != want.Enc || got.Rows != want.Rows ||
				got.Width != want.Width || got.DictLen != want.DictLen {
				t.Fatalf("case %d page %d: header got=%+v want=%+v", c, pg, got, want)
			}
			if string(got.Data) != string(want.Data) {
				t.Fatalf("case %d page %d: data got=%x want=%x", c, pg, got.Data, want.Data)
			}
			if string(rep.Data) != string(got.Data) {
				t.Fatalf("case %d page %d: replay divergence", c, pg)
			}

			if got.Enc == EncDict {
				if w.touches > 2*rows {
					t.Fatalf("case %d page %d: touches=%d > 2*rows=%d", c, pg, w.touches, 2*rows)
				}
				if w.touches != rows {
					t.Fatalf("case %d page %d: touches=%d want rows=%d", c, pg, w.touches, rows)
				}
			}

			dec, err := w.Decode(got)
			if err != nil {
				t.Fatalf("case %d page %d: decode %v", c, pg, err)
			}
			if !u32Equal(dec, values) {
				t.Fatalf("case %d page %d: round trip got=%v want=%v", c, pg, dec, values)
			}

			if w.fallback != ref.fallback || w.miss != ref.miss ||
				len(w.dictVals) != len(ref.dictVals) {
				t.Fatalf("case %d page %d: state divergence got(fb=%v miss=%d dl=%d) want(fb=%v miss=%d dl=%d)",
					c, pg, w.fallback, w.miss, len(w.dictVals),
					ref.fallback, ref.miss, len(ref.dictVals))
			}
		}
	}
}
