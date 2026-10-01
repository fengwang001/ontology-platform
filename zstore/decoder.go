package zstore

import "sync"

type decoderState int

const (
	stHeader decoderState = iota
	stBlockHeader
	stLen
	stData
	stTrailer
	stDone
)

// Decoder 是只含存储块的 zlib 容器流式解码器。
// 数据块内容边收边交付；并发安全。
type Decoder struct {
	mu        sync.Mutex
	state     decoderState
	err       error
	delivered []byte
	sum       adler32

	hdr       []byte // 已收集的头部字节（0-2）
	lenBuf    []byte // 已收集的 LEN/NLEN 字节（0-4）
	final     bool   // 当前块是否为终块
	remaining int    // 当前块剩余数据字节数
	trailer   []byte // 已收集的尾部字节（0-4）
}

// NewDecoder 返回一个处于头部等待状态的解码器。
func NewDecoder() *Decoder {
	return &Decoder{state: stHeader, sum: newAdler32()}
}

// Write 喂入字节流，边收边交付数据。
func (d *Decoder) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return 0, ErrPoisoned
	}
	n := 0
	for n < len(p) {
		if err := d.step(p, &n); err != nil {
			d.err = err
			return n, err
		}
	}
	return n, nil
}

// Close 校验流已完整读完尾部，否则报截断错误。
func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return ErrPoisoned
	}
	if d.state != stDone {
		d.err = ErrTruncated
		return ErrTruncated
	}
	return nil
}

// step 按当前状态尽量消费 p[*n:] 的字节。
// 调用方已持有锁。
func (d *Decoder) step(p []byte, n *int) error {
	switch d.state {
	case stHeader:
		need := 2 - len(d.hdr)
		c := min(need, len(p)-*n)
		d.hdr = append(d.hdr, p[*n:*n+c]...)
		*n += c
		if len(d.hdr) == 2 {
			if err := checkHeader(d.hdr[0], d.hdr[1]); err != nil {
				return err
			}
			d.state = stBlockHeader
		}
	case stBlockHeader:
		hdr := p[*n]
		*n++
		if hdr>>3 != 0 {
			return ErrBlockHeaderBits
		}
		switch (hdr >> 1) & 0x3 {
		case 0:
		case 3:
			return ErrBlockTypeReserved
		default:
			return ErrBlockTypeUnsupported
		}
		d.final = hdr&0x1 != 0
		d.lenBuf = d.lenBuf[:0]
		d.state = stLen
	case stLen:
		need := 4 - len(d.lenBuf)
		c := min(need, len(p)-*n)
		d.lenBuf = append(d.lenBuf, p[*n:*n+c]...)
		*n += c
		if len(d.lenBuf) == 4 {
			l := uint16(d.lenBuf[0]) | uint16(d.lenBuf[1])<<8
			nl := uint16(d.lenBuf[2]) | uint16(d.lenBuf[3])<<8
			if nl != ^l {
				return ErrNLEN
			}
			d.remaining = int(l)
			d.state = stData
			if d.remaining == 0 {
				d.finishBlock()
			}
		}
	case stData:
		c := min(d.remaining, len(p)-*n)
		chunk := p[*n : *n+c]
		*n += c
		d.sum.Write(chunk)
		d.delivered = append(d.delivered, chunk...)
		d.remaining -= c
		if d.remaining == 0 {
			d.finishBlock()
		}
	case stTrailer:
		need := 4 - len(d.trailer)
		c := min(need, len(p)-*n)
		d.trailer = append(d.trailer, p[*n:*n+c]...)
		*n += c
		if len(d.trailer) == 4 {
			want := d.sum.Sum32()
			got := uint32(d.trailer[0])<<24 | uint32(d.trailer[1])<<16 |
				uint32(d.trailer[2])<<8 | uint32(d.trailer[3])
			if got != want {
				return ErrChecksum
			}
			d.state = stDone
		}
	case stDone:
		return ErrTrailingData
	}
	return nil
}

// finishBlock 在一个块数据读完后切换到下一状态。
func (d *Decoder) finishBlock() {
	if d.final {
		d.state = stTrailer
	} else {
		d.state = stBlockHeader
	}
}

// checkHeader 按序校验 2 字节头，只报第一个错误。
func checkHeader(cmf, flg byte) error {
	if cmf&0x0f != 8 {
		return ErrMethod
	}
	if cmf>>4 > 7 {
		return ErrWindow
	}
	if (uint16(cmf)<<8|uint16(flg))%31 != 0 {
		return ErrHeaderCheck
	}
	if flg&0x20 != 0 {
		return ErrDict
	}
	return nil
}

// Bytes 返回已交付数据的副本。
func (d *Decoder) Bytes() []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.delivered...)
}

// Delivered 返回已交付的字节数。
func (d *Decoder) Delivered() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.delivered)
}
