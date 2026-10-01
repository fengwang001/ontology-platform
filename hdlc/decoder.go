package hdlc

import "sync"

type DropReason string

const (
	DropAlignment DropReason = "alignment"
	DropShort     DropReason = "short"
	DropFCS       DropReason = "fcs"
)

type Stats struct {
	Frames          uint64
	Aborts          uint64
	AlignmentErrors uint64
	ShortFrames     uint64
	FCSErrors       uint64
}

type Decoder struct {
	mu sync.Mutex

	inFrame      bool
	sharedStart  bool
	window       uint16
	windowBits   uint8
	physicalOnes uint8

	raw       []byte
	candidate []byte

	stats Stats
}

func NewDecoder() *Decoder {
	return &Decoder{}
}

func (d *Decoder) Write(p []byte) ([][]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var frames [][]byte
	for _, inputByte := range p {
		for bitIndex := 0; bitIndex < 8; bitIndex++ {
			frames = append(frames, d.writeBitLocked((inputByte>>bitIndex)&1)...)
		}
	}
	return frames, nil
}

func (d *Decoder) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

func (d *Decoder) writeBitLocked(bit byte) [][]byte {
	d.window = (d.window << 1) | uint16(bit)
	if d.windowBits < 8 {
		d.windowBits++
	}

	if !d.inFrame {
		if bit == 1 {
			d.physicalOnes++
		} else {
			d.physicalOnes = 0
		}
		if d.windowBits == 8 && d.window&0xFF == 0x7E {
			d.enterFrameLocked(false)
		}
		return nil
	}

	if d.windowBits == 8 && d.window&0xFF == 0x7E {
		return d.finishFrameLocked()
	}

	if bit == 1 {
		d.physicalOnes++
	} else {
		d.physicalOnes = 0
	}
	if d.physicalOnes >= 7 {
		d.abortFrameLocked()
		return nil
	}

	d.candidate = append(d.candidate, bit)
	if len(d.candidate) > 7 {
		d.raw = append(d.raw, d.candidate[0])
		d.candidate = d.candidate[1:]
	}
	return nil
}

func (d *Decoder) enterFrameLocked(sharedZero bool) {
	d.inFrame = true
	d.sharedStart = sharedZero
	d.window = 0
	d.windowBits = 0
	d.physicalOnes = 0
	d.raw = d.raw[:0]
	d.candidate = d.candidate[:0]
	if sharedZero {
		d.window = 0
		d.windowBits = 1
		d.raw = append(d.raw, 0)
	}
}

func (d *Decoder) abortFrameLocked() {
	d.stats.Aborts++
	d.inFrame = false
	d.sharedStart = false
	d.window = 0
	d.windowBits = 0
	d.physicalOnes = 0
	d.raw = d.raw[:0]
	d.candidate = d.candidate[:0]
}

func (d *Decoder) finishFrameLocked() [][]byte {
	raw := append([]byte(nil), d.raw...)
	if d.sharedStart {
		if len(raw) > 0 {
			raw = raw[1:]
		}
	}
	if len(d.candidate) >= 7 {
		raw = append(raw, d.candidate[:len(d.candidate)-7]...)
	}
	contentBits := destuffBits(raw)

	var delivered []byte
	if len(contentBits) == 0 {
		// Adjacent flags indicate an idle interval.
	} else if len(contentBits)%8 != 0 {
		d.stats.AlignmentErrors++
	} else if len(contentBits) < 24 {
		d.stats.ShortFrames++
	} else {
		content := bitsToBytes(contentBits)
		payload := content[:len(content)-2]
		receivedFCS := uint16(content[len(content)-2]) | uint16(content[len(content)-1])<<8
		if CRC16X25(payload) != receivedFCS {
			d.stats.FCSErrors++
		} else {
			delivered = append([]byte(nil), payload...)
			d.stats.Frames++
		}
	}

	d.enterFrameLocked(true)
	if delivered == nil {
		return nil
	}
	return [][]byte{delivered}
}

func bitsToBytes(bits []byte) []byte {
	result := make([]byte, len(bits)/8)
	for index, bit := range bits {
		result[index/8] |= bit << (index % 8)
	}
	return result
}

func destuffBits(raw []byte) []byte {
	content := make([]byte, 0, len(raw))
	ones := 0
	for _, bit := range raw {
		if bit == 0 {
			if ones == 5 {
				ones = 0
				continue
			}
			ones = 0
			content = append(content, 0)
			continue
		}
		ones++
		content = append(content, 1)
	}
	return content
}
