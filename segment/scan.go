package segment

import (
	"encoding/binary"
	"errors"
	"io"
	"os"

	"ontology/event"
)

// FrameReader reads frames from a seekable/streaming file region starting at
// base. Each field is read with exactly one ReadFull, so BytesRead equals the
// bytes physically touched from the frame region.
type FrameReader struct {
	f    *os.File
	base int64
	pos  int64
	read int64
}

// NewFrameReader reads frames from f beginning at byte offset base.
func NewFrameReader(f *os.File, base int64) *FrameReader {
	return &FrameReader{f: f, base: base}
}

// SeekTo positions the reader at a frame-region offset (absolute file offset
// is base+off) and resets the touched-byte counter.
func (fr *FrameReader) SeekTo(off int64) error {
	if _, err := fr.f.Seek(fr.base+off, io.SeekStart); err != nil {
		return err
	}
	fr.pos = off
	fr.read = 0
	return nil
}

// BytesRead reports frame-region bytes touched since the last SeekTo/open.
func (fr *FrameReader) BytesRead() int64 { return fr.read }

// Pos returns the current frame-region offset.
func (fr *FrameReader) Pos() int64 { return fr.pos }

func (fr *FrameReader) readFull(buf []byte) error {
	n, err := io.ReadFull(fr.f, buf)
	fr.read += int64(n)
	return err
}

// Next decodes one frame at the current position.
func (fr *FrameReader) Next() (event.Event, error) {
	lenBuf := make([]byte, event.LenSize)
	if err := fr.readFull(lenBuf); err != nil {
		if errors.Is(err, io.EOF) {
			return event.Event{}, io.EOF
		}
		return event.Event{}, event.ErrShortLength
	}
	plen := int(binary.BigEndian.Uint32(lenBuf))
	body := make([]byte, event.SeqSize+plen+event.CRCSize)
	if err := fr.readFull(body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return event.Event{}, event.ErrShortFrame
		}
		return event.Event{}, err
	}
	ev, _, err := event.Decode(append(lenBuf, body...))
	if err != nil {
		return event.Event{}, err
	}
	fr.pos += int64(event.FixedPrefix + plen + event.CRCSize)
	return ev, nil
}

// Inspect reports the header, the maximal run of intact frames and the first
// non-EOF error. Frame offsets are absolute file offsets.
type Inspect struct {
	Header   Header
	Events   []event.Event
	Offsets  []int64
	FrameEnd int64 // absolute offset just past the last intact frame
	Err      error
}

// InspectPath scans a segment file without trusting its header count.
func InspectPath(path string) (*Inspect, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return InspectBytes(raw)
}

// InspectBytes scans an in-memory segment image without trusting its count.
func InspectBytes(raw []byte) (*Inspect, error) {
	if len(raw) < HeaderSize {
		return nil, ErrShortHeader
	}
	if string(raw[:8]) != Magic {
		return nil, ErrBadMagic
	}
	h := Header{
		FirstSeq: binary.BigEndian.Uint64(raw[8:16]),
		Count:    binary.BigEndian.Uint32(raw[16:20]),
	}
	out := &Inspect{Header: h, FrameEnd: int64(HeaderSize)}
	pos := HeaderSize
	for pos < len(raw) {
		if len(raw)-pos < event.LenSize {
			out.Err = event.ErrShortLength
			break
		}
		plen := int(binary.BigEndian.Uint32(raw[pos : pos+event.LenSize]))
		frameLen := event.FixedPrefix + plen + event.CRCSize
		if len(raw)-pos < frameLen {
			out.Err = event.ErrShortFrame
			break
		}
		ev, _, err := event.Decode(raw[pos : pos+frameLen])
		if err != nil {
			out.Err = err
			break
		}
		off := int64(pos)
		out.Events = append(out.Events, ev)
		out.Offsets = append(out.Offsets, off)
		pos += frameLen
		out.FrameEnd = int64(pos)
	}
	return out, nil
}
