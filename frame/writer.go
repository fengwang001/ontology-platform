package frame

import "sync"

// Writer 把任意字节流切成带掩码校验和的块，并按压缩收益与历史失败
// 自适应决定每块是否压缩。所有方法可并发调用，结果等价于某个串行顺序。
//
// 每块（n 字节）按顺序判定：
//  1. 若跳过计数 skip > 0，则原样成块并 skip 减 1，减到 0 时置探测标记
//     probe = 真；
//  2. 否则若 n < 16，原样成块，不改变 f、skip、probe 与 curS；
//  3. 否则压缩得 comp，若 len(comp) < n - n/8（整数除法，恰等不算收益）
//     则用压缩块并令 f=0、probe=假、curS=S；否则用原样块：若 probe 为真
//     （探测失败）则 probe=假、curS=min(2*curS, 8*S)、skip=curS、f=0，
//     若 probe 为假则 f 加 1，f 等于 F 时令 skip=curS 并把 f 清零。
type Writer struct {
	mu  sync.Mutex
	f   int // 连续压缩失败计数
	s   int // 基础跳过步长 S
	thr int // 连续失败阈值 F

	skip  int  // 剩余强制原样块数
	curS  int  // 当前跳过步长
	probe bool // 下一块为探测块

	buf        []byte // 待成块的未压缩数据（至多 65536）
	out        []byte // 已写出的全部字节
	hdrWritten bool   // 标识块是否已写出
	closed     bool
}

// NewWriter 创建写出器。F >= 1、S >= 1，否则返回 ErrParam。
func NewWriter(F, S int) (*Writer, error) {
	if F < 1 || S < 1 {
		return nil, ErrParam
	}
	return &Writer{thr: F, s: S, curS: S}, nil
}

// Write 把 p 放入缓冲，缓冲恰满 65536 字节时立即成块。
// 输出与 Write 的切分无关。封闭后调用返回 ErrClosed，且不改变状态。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, ErrClosed
	}
	n := len(p)
	for len(p) > 0 {
		k := maxUncompressed - len(w.buf)
		if k > len(p) {
			k = len(p)
		}
		w.buf = append(w.buf, p[:k]...)
		p = p[k:]
		if len(w.buf) == maxUncompressed {
			w.emitChunk()
		}
	}
	return n, nil
}

// Flush 把缓冲中非空的内容成块。封闭后调用返回 ErrClosed。
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if len(w.buf) > 0 {
		w.emitChunk()
	}
	return nil
}

// Close 先 Flush 再封闭。若整个流没有任何数据，只写出标识块。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if len(w.buf) > 0 {
		w.emitChunk()
	}
	if !w.hdrWritten {
		w.out = append(w.out, identifierChunk...)
		w.hdrWritten = true
	}
	w.closed = true
	return nil
}

// Bytes 返回已写出的全部字节副本。
func (w *Writer) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.out))
	copy(out, w.out)
	return out
}

// emitChunk 把 w.buf 中的内容按自适应规则成块并清空缓冲。调用方须持有锁。
func (w *Writer) emitChunk() {
	if !w.hdrWritten {
		w.out = append(w.out, identifierChunk...)
		w.hdrWritten = true
	}
	n := len(w.buf)
	typ := byte(chunkRaw)
	var comp []byte
	compressed := false

	switch {
	case w.skip > 0:
		// 强制原样；skip 减到 0 时置探测标记。
		w.skip--
		if w.skip == 0 {
			w.probe = true
		}
	case n < minCompressTry:
		// 原样成块，不改变 f、skip、probe 与 curS。
	default:
		comp = compress(w.buf)
		if len(comp) < n-n/8 {
			compressed = true
			w.f = 0
			w.probe = false
			w.curS = w.s
		} else if w.probe {
			// 探测失败：步长翻倍并封顶 8S，立即进入跳过。
			w.probe = false
			w.curS = min(2*w.curS, 8*w.s)
			w.skip = w.curS
			w.f = 0
		} else {
			w.f++
			if w.f == w.thr {
				w.skip = w.curS
				w.f = 0
			}
		}
	}

	data := w.buf
	if compressed {
		typ = chunkCompressed
		data = comp
	}
	payloadLen := 4 + len(data)
	w.out = appendHeader(w.out, typ, payloadLen)
	sum := maskedChecksum(w.buf)
	w.out = append(w.out, byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
	w.out = append(w.out, data...)
	w.buf = w.buf[:0]
}
