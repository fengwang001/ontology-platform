package ontology

import (
	"encoding/binary"
	"math/bits"
	"sync"
)

// Writer encodes one unsigned integer column one page at a time.
type Writer struct {
	mu      sync.Mutex
	maxDict int
	maxRows int

	dict     map[uint32]int
	dictVals []uint32

	fallback bool
	miss     int
	touches  int

	buf []uint32
}

// New constructs a Writer with the given dictionary and row limits.
func New(maxDict, maxRows int) (*Writer, error) {
	if maxDict < 1 || maxDict > 65536 || maxRows < 1 || maxRows > 65536 {
		return nil, ErrParam
	}
	return &Writer{
		maxDict: maxDict,
		maxRows: maxRows,
		dict:    make(map[uint32]int),
		buf:     make([]uint32, 0, maxRows),
	}, nil
}

// Append buffers one value for the next page.
func (w *Writer) Append(v uint32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) >= w.maxRows {
		return ErrFull
	}
	w.buf = append(w.buf, v)
	return nil
}

// Flush encodes the buffered rows as one page and clears the buffer.
func (w *Writer) Flush() (Page, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rows := len(w.buf)
	if rows == 0 {
		return Page{}, ErrEmpty
	}
	values := w.buf
	w.buf = make([]uint32, 0, w.maxRows)

	if w.fallback {
		return w.plainPage(values), nil
	}

	// Tentatively extend the dictionary with this page's unseen values, in
	// first-occurrence order.
	indices := make([]int, rows)
	var newValues []uint32
	merged := make(map[uint32]int, len(w.dict))
	for k, v := range w.dict {
		merged[k] = v
	}
	for i, v := range values {
		idx, ok := merged[v]
		if !ok {
			idx = len(w.dictVals) + len(newValues)
			merged[v] = idx
			newValues = append(newValues, v)
		}
		indices[i] = idx
	}
	distinct := len(w.dictVals) + len(newValues)

	if distinct > w.maxDict {
		w.fallback = true
		return w.plainPage(values), nil
	}

	width := 0
	if distinct > 1 {
		width = bits.Len(uint(distinct - 1))
	}

	w.touches = 0
	hybrid := hybridEncode(indices, width, &w.touches)

	dictSize := 1 + len(hybrid) + 4*len(newValues)
	plainSize := 4 * rows

	if dictSize < plainSize {
		// Commit the tentative dictionary entries.
		for _, v := range newValues {
			w.dict[v] = len(w.dictVals)
			w.dictVals = append(w.dictVals, v)
		}
		w.miss = 0

		data := make([]byte, 1+len(hybrid))
		data[0] = byte(width)
		copy(data[1:], hybrid)
		return Page{
			Enc:     EncDict,
			Rows:    rows,
			Width:   width,
			DictLen: len(w.dictVals),
			Data:    data,
		}, nil
	}

	// Dict page loses (or ties): dictionary is left untouched.
	w.miss++
	if w.miss >= 3 {
		w.fallback = true
	}
	return w.plainPage(values), nil
}

// Decode reconstructs the values of a previously written page.
func (w *Writer) Decode(p Page) ([]uint32, error) {
	w.mu.Lock()
	dictVals := append([]uint32(nil), w.dictVals...)
	w.mu.Unlock()

	switch p.Enc {
	case EncPlain:
		if p.Width != 0 || p.DictLen != 0 || len(p.Data) != 4*p.Rows {
			return nil, ErrCorrupt
		}
		out := make([]uint32, p.Rows)
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(p.Data[4*i:])
		}
		return out, nil

	case EncDict:
		if p.Rows < 0 || p.DictLen < 0 || p.DictLen > len(dictVals) ||
			p.Width < 0 || p.Width > 64 || len(p.Data) < 1 {
			return nil, ErrCorrupt
		}
		if int(p.Data[0]) != p.Width {
			return nil, ErrCorrupt
		}
		idxs, err := hybridDecode(p.Data[1:], p.Width, p.DictLen, p.Rows)
		if err != nil {
			return nil, err
		}
		out := make([]uint32, len(idxs))
		for i, idx := range idxs {
			out[i] = dictVals[idx]
		}
		return out, nil

	default:
		return nil, ErrCorrupt
	}
}

func (w *Writer) plainPage(values []uint32) Page {
	data := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(data[4*i:], v)
	}
	return Page{
		Enc:  EncPlain,
		Rows: len(values),
		Data: data,
	}
}
