package span

import (
	"errors"
	"fmt"
)

var ErrRange = errors.New("span: offset out of range")

type Segment struct {
	OrigStart int
	OrigEnd   int
	OutStart  int
}

type Map struct {
	segments []Segment
	origLen  int
	outLen   int
	lastScan int
}

func NewMap(origLen, outLen int) *Map {
	return &Map{origLen: origLen, outLen: outLen}
}

func (m *Map) Append(segments []Segment, origLen, outLen int) {
	if len(segments) == 0 {
		m.origLen += origLen
		m.outLen += outLen
		return
	}
	for _, seg := range segments {
		m.segments = append(m.segments, Segment{
			OrigStart: seg.OrigStart + m.origLen,
			OrigEnd:   seg.OrigEnd + m.origLen,
			OutStart:  seg.OutStart + m.outLen,
		})
	}
	m.origLen += origLen
	m.outLen += outLen
}

func (m *Map) Delete(origStart, origEnd, outAt int) error {
	if origStart < 0 || origEnd < origStart || outAt < 0 {
		return ErrRange
	}
	if n := len(m.segments); n > 0 {
		last := m.segments[n-1]
		if outAt < last.OutStart {
			return ErrRange
		}
		if origStart == last.OrigEnd && outAt == last.OutStart {
			m.segments[n-1].OrigEnd = origEnd
			return nil
		}
		if origStart < last.OrigEnd {
			return ErrRange
		}
	}
	m.segments = append(m.segments, Segment{origStart, origEnd, outAt})
	return nil
}

func (m *Map) Finish(origLen, outLen int) error {
	for _, seg := range m.segments {
		if seg.OutStart > outLen || seg.OrigEnd > origLen {
			return ErrRange
		}
	}
	m.origLen = origLen
	m.outLen = outLen
	return nil
}

func (m *Map) OrigLen() int { return m.origLen }
func (m *Map) OutLen() int  { return m.outLen }

func (m *Map) segmentsSnapshot() []Segment {
	out := make([]Segment, len(m.segments))
	copy(out, m.segments)
	return out
}

func (m *Map) ToOrig(outOffset int) (int, error) {
	if outOffset < 0 || outOffset > m.outLen {
		return 0, fmt.Errorf("%w: output offset %d", ErrRange, outOffset)
	}
	lo, hi := 0, len(m.segments)
	for lo < hi {
		m.lastScan++
		mid := lo + (hi-lo)/2
		if m.segments[mid].OutStart < outOffset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastScan++
	origOffset := outOffset
	if lo > 0 {
		seg := m.segments[lo-1]
		if outOffset == seg.OutStart {
			return seg.OrigEnd, nil
		}
		origOffset += seg.OrigEnd - seg.OutStart
	}
	return origOffset, nil
}

func (m *Map) ToOut(origOffset int) (int, error) {
	if origOffset < 0 || origOffset > m.origLen {
		return 0, fmt.Errorf("%w: original offset %d", ErrRange, origOffset)
	}
	lo, hi := 0, len(m.segments)
	for lo < hi {
		m.lastScan++
		mid := lo + (hi-lo)/2
		if m.segments[mid].OrigEnd < origOffset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastScan++
	outOffset := origOffset
	if lo < len(m.segments) {
		seg := m.segments[lo]
		if origOffset >= seg.OrigStart {
			return seg.OutStart, nil
		}
	}
	if lo > 0 {
		seg := m.segments[lo-1]
		outOffset -= seg.OrigEnd - seg.OutStart
	}
	return outOffset, nil
}

func (m *Map) LastScan() int { return m.lastScan }

func (m *Map) SegmentCount() int { return len(m.segments) }
