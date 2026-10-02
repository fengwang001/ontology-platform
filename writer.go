package ontology

import (
	"encoding/binary"
	"sync"
)

var identifierChunk = []byte{0xFF, 0x06, 0x00, 0x00, 's', 'N', 'a', 'P', 'p', 'Y'}

// Writer 把任意字节流切成带掩码校验和的数据块，并根据压缩收益与
// 历史失败自适应决定每块是否压缩。所有方法可并发调用。
//
// 输出只取决于写入的字节以及 Flush/Close 的位置，与 Write 的切分无关。
type Writer struct {
	mu sync.Mutex

	f, s       int
	curS, skip int
	fail       int
	probe      bool

	buf    []byte
	out    []byte
	closed bool
}

// NewWriter 创建写出器。F>=1 为连续压缩失败计数上限，S>=1 为初始
// 跳过长度（探测失败后翻倍，封顶 8S）。参数非法返回 ErrParam。
func NewWriter(F, S int) (*Writer, error) {
	if F < 1 || S < 1 {
		return nil, ErrParam
	}
	return &Writer{f: F, s: S, curS: S}, nil
}

// Write 把字节放入缓冲，缓冲恰满 65536 字节时立即成块。
// Writer 关闭后返回 ErrClosed。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}
	w.buf = append(w.buf, p...)
	for len(w.buf) >= maxChunkData {
		w.emitChunk(w.buf[:maxChunkData:maxChunkData])
		w.buf = w.buf[maxChunkData:]
	}
	return len(p), nil
}

// Flush 把缓冲中非空的内容成块。空缓冲时不产生任何数据块。
// Writer 关闭后返回 ErrClosed。
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if len(w.buf) > 0 {
		chunk := w.buf
		w.buf = nil
		w.emitChunk(chunk)
	}
	return nil
}

// Close 先 Flush 再封闭。若整个流没有任何数据，只写出标识块。
// 封闭后 Write、Flush、Close 均返回 ErrClosed。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if len(w.buf) > 0 {
		chunk := w.buf
		w.buf = nil
		w.emitChunk(chunk)
	}
	w.ensureIdentifier()
	w.closed = true
	return nil
}

// Bytes 返回已写出全部字节的副本。
func (w *Writer) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.out))
	copy(out, w.out)
	return out
}

// ensureIdentifier 在第一个数据块之前写出一次标识块。
func (w *Writer) ensureIdentifier() {
	if len(w.out) == 0 {
		w.out = append(w.out, identifierChunk...)
	}
}

// emitChunk 按自适应跳过规则决定一个数据块是否压缩并写出。
func (w *Writer) emitChunk(data []byte) {
	w.ensureIdentifier()
	n := len(data)
	useRLE := false
	comp := rleEncode(data)

	switch {
	case w.skip > 0:
		w.skip--
		if w.skip == 0 {
			w.probe = true
		}
	case n < 16:
		// 小块不尝试压缩，不改变 f、skip、probe、curS。
	default:
		if len(comp) < n-n/8 {
			useRLE = true
			w.fail = 0
			w.probe = false
			w.curS = w.s
		} else {
			if w.probe {
				w.probe = false
				w.curS *= 2
				if capS := 8 * w.s; w.curS > capS {
					w.curS = capS
				}
				w.skip = w.curS
				w.fail = 0
			} else {
				w.fail++
				if w.fail == w.f {
					w.skip = w.curS
					w.fail = 0
				}
			}
		}
	}

	payload := data
	chunkType := byte(chunkRaw)
	if useRLE {
		chunkType = chunkRLE
		payload = comp
	}

	total := len(payload) + 4
	w.out = append(w.out, chunkType, byte(total), byte(total>>8), byte(total>>16))
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], maskedChecksum(data))
	w.out = append(w.out, sum[:]...)
	w.out = append(w.out, payload...)
}
