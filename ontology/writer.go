package ontology

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sync"
)

const (
	Plain Enc = iota
	Dict
)

type Enc int

type Page struct {
	Enc     Enc
	Rows    int
	Width   int
	DictLen int
	Data    []byte
}

var (
	ErrParam   = errors.New("ontology: invalid parameter")
	ErrFull    = errors.New("ontology: buffer full")
	ErrEmpty   = errors.New("ontology: buffer empty")
	ErrCorrupt = errors.New("ontology: corrupt page")
)

const (
	maxDictLimit = 65536
	maxRowsLimit = 65536
)

type Writer struct {
	mu       sync.Mutex
	maxDict  int
	maxRows  int
	dict     map[uint32]uint32
	dictVals []uint32
	fallback bool
	miss     int
	buf      []uint32
	touches  int
}

func New(maxDict, maxRows int) (*Writer, error) {
	if maxDict < 1 || maxDict > maxDictLimit || maxRows > maxRowsLimit || maxRows < 1 {
		return nil, ErrParam
	}
	return &Writer{
		maxDict: maxDict,
		maxRows: maxRows,
		dict:    make(map[uint32]uint32),
	}, nil
}

func (w *Writer) Append(v uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) >= w.maxRows {
		return ErrFull
	}
	w.buf = append(w.buf, v)
	return nil
}

func (w *Writer) Flush() (Page, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return Page{}, ErrEmpty
	}
	rows := len(w.buf)
	w.touches = 0

	// 第一步：已降级则直接出 Plain 页（不动 miss）。
	if w.fallback {
		return w.plainPage(rows), nil
	}

	// 试算：本页新值按首次出现次序接在已提交字典之后。
	idx := make([]uint32, rows)
	trial := make(map[uint32]uint32)
	var newVals []uint32
	next := uint32(len(w.dictVals))
	for i, v := range w.buf {
		if id, ok := w.dict[v]; ok {
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
	w.touches += rows // 映射每行读取一次索引

	// 第二步：合并后不同值个数超过 maxDict，粘滞降级，字典不变。
	d := len(w.dictVals) + len(newVals)
	if d > w.maxDict {
		w.fallback = true
		return w.plainPage(rows), nil
	}

	// 第三步：推导宽度并做混合编码，按页大小裁决。
	width := 0
	if d > 1 {
		width = bits.Len(uint(d - 1))
	}
	w.touches += rows // 编码扫描每行再读一次索引
	h := hybridEncode(idx, width)
	dictSize := 1 + len(h) + 4*len(newVals)
	plainSize := 4 * rows
	if dictSize < plainSize {
		for _, v := range newVals {
			w.dict[v] = uint32(len(w.dictVals))
			w.dictVals = append(w.dictVals, v)
		}
		w.miss = 0
		data := make([]byte, 0, 1+len(h))
		data = append(data, byte(width))
		data = append(data, h...)
		w.buf = w.buf[:0]
		return Page{Enc: Dict, Rows: rows, Width: width, DictLen: len(w.dictVals), Data: data}, nil
	}
	w.miss++
	if w.miss >= 3 {
		w.fallback = true
	}
	return w.plainPage(rows), nil
}

// plainPage 输出 Plain 页并清空缓冲；不改变字典与 miss。
// 调用方必须已持有锁。
func (w *Writer) plainPage(rows int) Page {
	data := make([]byte, 4*rows)
	for i, v := range w.buf {
		binary.LittleEndian.PutUint32(data[i*4:], v)
	}
	w.buf = w.buf[:0]
	return Page{Enc: Plain, Rows: rows, Width: 0, DictLen: 0, Data: data}
}

func (w *Writer) Decode(p Page) ([]uint32, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch p.Enc {
	case Plain:
		if len(p.Data) != 4*p.Rows || p.Rows < 0 {
			return nil, ErrCorrupt
		}
		out := make([]uint32, p.Rows)
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(p.Data[i*4:])
		}
		return out, nil
	case Dict:
		return w.decodeDict(p)
	default:
		return nil, ErrCorrupt
	}
}

// decodeDict 解码 Dict 页并做完整校验。调用方必须已持有锁。
func (w *Writer) decodeDict(p Page) ([]uint32, error) {
	if p.Rows < 0 || p.Width < 0 || p.Width > 16 || p.DictLen < 0 {
		return nil, ErrCorrupt
	}
	if len(p.Data) == 0 || int(p.Data[0]) != p.Width {
		return nil, ErrCorrupt
	}
	body := p.Data[1:]
	out := make([]uint32, 0, p.Rows)
	pos := 0
	for pos < len(body) {
		hdr, n := binary.Uvarint(body[pos:])
		if n <= 0 {
			return nil, ErrCorrupt
		}
		pos += n
		if hdr&1 == 0 {
			count := hdr >> 1
			if count == 0 {
				return nil, ErrCorrupt
			}
			nb := (p.Width + 7) / 8
			if len(body)-pos < nb {
				return nil, ErrCorrupt
			}
			var v uint32
			for k := 0; k < nb; k++ {
				v |= uint32(body[pos+k]) << (8 * k)
			}
			pos += nb
			if uint64(len(out))+count > uint64(p.Rows) {
				return nil, ErrCorrupt
			}
			val, err := w.dictValue(v, p.DictLen)
			if err != nil {
				return nil, err
			}
			for k := uint64(0); k < count; k++ {
				out = append(out, val)
			}
			continue
		}
		groups := hdr >> 1
		if groups == 0 {
			return nil, ErrCorrupt
		}
		need := int(groups) * p.Width
		if len(body)-pos < need {
			return nil, ErrCorrupt
		}
		vals := unpackValues(body[pos:pos+need], int(groups), p.Width)
		pos += need
		real := len(vals)
		if pos == len(body) {
			// 仅最后一个位打包游程的末组允许用索引 0 补足。
			real = p.Rows - len(out)
			if real <= 0 || real > len(vals) || len(vals)-real >= 8 {
				return nil, ErrCorrupt
			}
			for _, pv := range vals[real:] {
				if pv != 0 {
					return nil, ErrCorrupt
				}
			}
		} else if len(out)+len(vals) > p.Rows {
			return nil, ErrCorrupt
		}
		for _, v := range vals[:real] {
			val, err := w.dictValue(v, p.DictLen)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
	}
	if len(out) != p.Rows {
		return nil, ErrCorrupt
	}
	return out, nil
}

// dictValue 校验索引并查已提交字典。
func (w *Writer) dictValue(idx uint32, dictLen int) (uint32, error) {
	if int(idx) >= dictLen || int(idx) >= len(w.dictVals) {
		return 0, ErrCorrupt
	}
	return w.dictVals[idx], nil
}

// unpackValues 从 data 解出 groups*8 个 w 位索引（低位在前，字节小端）。
func unpackValues(data []byte, groups, w int) []uint32 {
	vals := make([]uint32, 0, groups*8)
	var acc uint64
	var nbits uint
	pos := 0
	mask := uint64(1)<<uint(w) - 1
	for i := 0; i < groups*8; i++ {
		for nbits < uint(w) {
			acc |= uint64(data[pos]) << nbits
			nbits += 8
			pos++
		}
		vals = append(vals, uint32(acc&mask))
		acc >>= uint(w)
		nbits -= uint(w)
	}
	return vals
}
