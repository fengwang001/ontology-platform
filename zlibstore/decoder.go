package zlibstore

import (
	"fmt"
	"sync"
)

// 解码器主状态：头部 → 若干块 → 4 字节尾部 → 完成。
const (
	dStateHeader  = iota // 消费 2 字节 zlib 头
	dStateBlock          // 消费 1 字节块头
	dStateMeta           // 消费 4 字节 LEN/NLEN
	dStateData           // 透传 length 字节块数据
	dStateTrailer        // 消费 4 字节 Adler-32
	dStateDone           // 尾部已完整读入并校验
)

// Decoder 是只用存储块的 zlib 容器流式解码器。
//
// 调用 Write 喂入任意切分的字节（允许逐字节），解码出的数据块内容
// 从同一调用返回、边收边交付；解码结果与输入切分方式无关。
// Close 宣告输入结束，若此时尚未读完 4 字节尾部则返回 ErrTruncated。
//
// 任何错误都会让解码器进入粘滞失败态：此后 Write/Close 一律返回
// ErrPoisoned 且不改变任何状态。Delivered 始终可查询已交付字节数。
//
// Decoder 的所有方法可被并发调用，效果等价于某种串行顺序。
type Decoder struct {
	mu sync.Mutex

	state     int
	poisoned  bool
	firstErr  error // 首次失败的错误，粘滞期返回的 ErrPoisoned 会包装它
	delivered int64
	adler     adlerState

	// pending 保存跨 Write 边界尚未消费完的控制字节（头/块头/LEN/NLEN/尾部）。
	// 数据负载不进这里，而是在 dStateData 下直接从输入切片交付，
	// 因此缓存规模恒为有界的小常数。
	pending     []byte
	pendingNeed int

	// dStateData 时当前块还需交付的字节数。
	blockRemain int
	// blockFinal 记录当前块的 BFINAL 位，供块结束时决定去向。
	blockFinal byte
}

// NewDecoder 创建解码器。
func NewDecoder() *Decoder {
	d := &Decoder{state: dStateHeader, adler: newAdler()}
	d.setNeed(headerLen)
	return d
}

// Write 消费输入字节，返回本次新交付的数据与遇到的第一个错误。
func (d *Decoder) Write(p []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.poisoned {
		return nil, d.poisonErr()
	}

	out := make([]byte, 0, len(p))

	for len(p) > 0 {
		switch d.state {
		case dStateHeader, dStateBlock, dStateMeta, dStateTrailer:
			consumed, complete, err := d.collectControl(p)
			p = p[consumed:]
			if err != nil {
				d.poison(err)
				return out, err
			}
			if !complete {
				break // 控制字段尚未收齐，等待后续 Write
			}
			if err := d.dispatchControl(); err != nil {
				d.poison(err)
				return out, err
			}

		case dStateData:
			n := min(len(p), d.blockRemain)
			chunk := p[:n]
			d.adler.update(chunk)
			out = append(out, chunk...)
			d.delivered += int64(n)
			d.blockRemain -= n
			p = p[n:]
			if d.blockRemain == 0 {
				d.afterBlock()
			}

		case dStateDone:
			// 终块与尾部已结束，任何剩余字节都是尾部之后的垃圾。
			d.poison(ErrTrailingBytes)
			return out, ErrTrailingBytes
		}
	}

	return out, nil
}

// Close 校验流是否完整结束（终块后恰好 4 字节尾部）。
func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.poisoned {
		return d.poisonErr()
	}
	if d.state != dStateDone {
		err := ErrTruncated
		d.poison(err)
		return err
	}
	return nil
}

// Delivered 返回截至目前已交付给调用方的字节总数。
func (d *Decoder) Delivered() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.delivered
}

const (
	headerLen       = 2
	blockHeaderLen  = 1
	blockMetaLen    = 4
	trailerLen      = 4
	dictFlagMask    = 0x20
	blockHeaderMask = 0xF8 // 块头高 5 位必须为 0
	blockTypeMask   = 0x06
	blockTypeShift  = 1
	bfinalBit       = 0x01
)

// setNeed 安排下一个长度为 need 的控制字段的收集。
func (d *Decoder) setNeed(need int) {
	d.pendingNeed = need
	if cap(d.pending) < need {
		d.pending = make([]byte, 0, need)
	} else {
		d.pending = d.pending[:0]
	}
}

// collectControl 从 p 向 d.pending 搬运字节，直到收满 pendingNeed。
// 返回本次消费的字节数以及字段是否已经收齐。
func (d *Decoder) collectControl(p []byte) (int, bool, error) {
	need := d.pendingNeed - len(d.pending)
	n := min(len(p), need)
	d.pending = append(d.pending, p[:n]...)
	if len(d.pending) < d.pendingNeed {
		return n, false, nil
	}
	return n, true, nil
}

// dispatchControl 解析刚收齐的控制字段并推进状态。
func (d *Decoder) dispatchControl() error {
	switch d.state {
	case dStateHeader:
		return d.consumeHeader()
	case dStateBlock:
		return d.consumeBlockHeader()
	case dStateMeta:
		return d.consumeMeta()
	case dStateTrailer:
		return d.consumeTrailer()
	}
	return nil
}

// consumeHeader 按固定顺序只报第一个违规：
// 压缩方法、窗口信息、16 位整除校验、字典标志。
func (d *Decoder) consumeHeader() error {
	cmf, flg := d.pending[0], d.pending[1]
	if cmf&0x0F != 8 {
		return ErrBadMethod
	}
	if cmf>>4 > 7 {
		return ErrBadWindow
	}
	if (uint16(cmf)<<8|uint16(flg))%31 != 0 {
		return ErrBadCheckValue
	}
	if flg&dictFlagMask != 0 {
		return ErrDictionaryPresent
	}
	d.state = dStateBlock
	d.setNeed(blockHeaderLen)
	return nil
}

// consumeBlockHeader 校验块头并决定下一块形态。
func (d *Decoder) consumeBlockHeader() error {
	bh := d.pending[0]
	if bh&blockHeaderMask != 0 {
		return ErrBlockHeaderReserved
	}
	switch (bh & blockTypeMask) >> blockTypeShift {
	case 0: // 存储块，继续读 LEN/NLEN
		d.state = dStateMeta
		d.setNeed(blockMetaLen)
	case 1:
		return ErrBlockFixedHuffman
	case 2:
		return ErrBlockDynamicHuffman
	default:
		return ErrBlockReservedType
	}
	d.blockFinal = bh & bfinalBit
	return nil
}

// consumeMeta 校验 LEN/NLEN 互补关系；非终块 LEN 允许为 0。
func (d *Decoder) consumeMeta() error {
	length := uint16(d.pending[0]) | uint16(d.pending[1])<<8
	nlen := uint16(d.pending[2]) | uint16(d.pending[3])<<8
	if nlen != ^length {
		return ErrBadNLEN
	}
	if length == 0 {
		// 空块直接进入下一块头或尾部。
		d.afterBlock()
	} else {
		d.blockRemain = int(length)
		d.state = dStateData
	}
	return nil
}

// afterBlock 在一块数据（含 LEN=0 的块）结束后按 BFINAL 决定去向。
func (d *Decoder) afterBlock() {
	if d.blockFinal != 0 {
		d.state = dStateTrailer
		d.setNeed(trailerLen)
	} else {
		d.state = dStateBlock
		d.setNeed(blockHeaderLen)
	}
}

// consumeTrailer 比对 4 字节大端 Adler-32 与已交付数据的校验和。
func (d *Decoder) consumeTrailer() error {
	want := uint32(d.pending[0])<<24 |
		uint32(d.pending[1])<<16 |
		uint32(d.pending[2])<<8 |
		uint32(d.pending[3])
	if want != d.adler.sum() {
		return ErrChecksum
	}
	d.state = dStateDone
	return nil
}

// poison 进入粘滞失败态并记录首个错误。
func (d *Decoder) poison(err error) {
	d.poisoned = true
	d.firstErr = err
}

// poisonErr 生成包装了首个错误原因的 ErrPoisoned，
// 粘滞期调用只返回 ErrPoisoned 本身：首个错误只作为日志线索附加，
// 不再让 errors.Is 与首错误成立，避免与“只报第一个错误”混淆。
func (d *Decoder) poisonErr() error {
	return fmt.Errorf("%w (first error: %v)", ErrPoisoned, d.firstErr)
}
