package ontology

import (
	"encoding/binary"
	"math/bits"
)

// naiveWriter 是按规格逐条直译的朴素模拟器，用于与 Writer 对照。
// 它独立实现判定次序、混合编码与位打包（逐位写入），不与实现共享代码。
type naiveWriter struct {
	maxDict  int
	maxRows  int
	dictVals []uint32
	pos      map[uint32]uint32
	fallback bool
	miss     int
	buf      []uint32
}

func newNaive(maxDict, maxRows int) *naiveWriter {
	return &naiveWriter{
		maxDict: maxDict,
		maxRows: maxRows,
		pos:     make(map[uint32]uint32),
	}
}

func (n *naiveWriter) append(v uint32) error {
	if len(n.buf) >= n.maxRows {
		return ErrFull
	}
	n.buf = append(n.buf, v)
	return nil
}

// naiveDecision 记录一次 Flush 的判定依据，供日志打印。
type naiveDecision struct {
	d, width, nNew, hLen int
	dictSize, plainSize  int
	via                  string
}

func (n *naiveWriter) flush() (Page, naiveDecision, error) {
	var dec naiveDecision
	if len(n.buf) == 0 {
		return Page{}, dec, ErrEmpty
	}
	rows := len(n.buf)
	dec.plainSize = 4 * rows
	plain := func(via string) (Page, naiveDecision, error) {
		data := make([]byte, 4*rows)
		for i, v := range n.buf {
			binary.LittleEndian.PutUint32(data[i*4:], v)
		}
		n.buf = n.buf[:0]
		dec.via = via
		return Page{Enc: Plain, Rows: rows, Width: 0, DictLen: 0, Data: data}, dec, nil
	}
	if n.fallback {
		return plain("step1:fallback")
	}
	idx := make([]uint32, rows)
	trial := make(map[uint32]uint32)
	var newVals []uint32
	next := uint32(len(n.dictVals))
	for i, v := range n.buf {
		if id, ok := n.pos[v]; ok {
			idx[i] = id
			continue
		}
		if id, ok := trial[v]; ok {
			idx[i] = id
			continue
		}
		trial[v] = next
		idx[i] = next
		next++
		newVals = append(newVals, v)
	}
	dec.d = len(n.dictVals) + len(newVals)
	dec.nNew = len(newVals)
	if dec.d > n.maxDict {
		n.fallback = true
		return plain("step2:D>maxDict")
	}
	if dec.d > 1 {
		dec.width = bits.Len(uint(dec.d - 1))
	}
	h := naiveHybrid(idx, dec.width)
	dec.hLen = len(h)
	dec.dictSize = 1 + len(h) + 4*len(newVals)
	if dec.dictSize < dec.plainSize {
		for _, v := range newVals {
			n.pos[v] = uint32(len(n.dictVals))
			n.dictVals = append(n.dictVals, v)
		}
		n.miss = 0
		data := append([]byte{byte(dec.width)}, h...)
		n.buf = n.buf[:0]
		dec.via = "step3:dict"
		return Page{Enc: Dict, Rows: rows, Width: dec.width, DictLen: len(n.dictVals), Data: data}, dec, nil
	}
	n.miss++
	if n.miss >= 3 {
		n.fallback = true
	}
	return plain("step3:size")
}

// naiveHybrid 逐位实现的混合编码。
func naiveHybrid(idx []uint32, w int) []byte {
	var out []byte
	var pend []uint32
	emit := func(final bool) {
		if len(pend) == 0 {
			return
		}
		q := append([]uint32(nil), pend...)
		if final {
			for len(q)%8 != 0 {
				q = append(q, 0)
			}
		}
		groups := len(q) / 8
		out = binary.AppendUvarint(out, uint64(groups)<<1|1)
		acc := make([]byte, groups*w)
		for i, v := range q {
			for b := 0; b < w; b++ {
				if v>>uint(b)&1 == 1 {
					pos := i*w + b
					acc[pos/8] |= 1 << uint(pos%8)
				}
			}
		}
		out = append(out, acc...)
		pend = pend[:0]
	}
	i := 0
	for i < len(idx) {
		j := i + 1
		for j < len(idx) && idx[j] == idx[i] {
			j++
		}
		x, l := idx[i], j-i
		if l >= 8 {
			f := (8 - len(pend)%8) % 8
			if l-f >= 8 {
				for k := 0; k < f; k++ {
					pend = append(pend, x)
				}
				emit(false)
				out = binary.AppendUvarint(out, uint64(l-f)<<1)
				for k := 0; k < (w+7)/8; k++ {
					out = append(out, byte(x>>(8*k)))
				}
			} else {
				for k := 0; k < l; k++ {
					pend = append(pend, x)
				}
			}
		} else {
			for k := 0; k < l; k++ {
				pend = append(pend, x)
			}
		}
		i = j
	}
	emit(true)
	return out
}
