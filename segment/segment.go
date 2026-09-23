// Package segment 实现段文件的追加写与顺序读。
//
// 布局（小端）：自描述头 24B = magic "OSEG" + version u32 + firstSeq u64 +
// count u64，随后逐事件记录（见 event 包）。段内第 i 条事件序号为
// firstSeq+i，记录本身不存序号。
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"ontology/event"
)

const (
	// HeaderSize 是段头字节数。
	HeaderSize = 24
	magic      = "OSEG"
	version    = 1
)

// ErrHeaderIncomplete：文件不足一个完整段头。
var ErrHeaderIncomplete = errors.New("segment: header incomplete")

// Header 是段头内容。
type Header struct {
	FirstSeq uint64
	Count    uint64
}

func encodeHeader(h Header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf, magic)
	binary.LittleEndian.PutUint32(buf[4:], version)
	binary.LittleEndian.PutUint64(buf[8:], h.FirstSeq)
	binary.LittleEndian.PutUint64(buf[16:], h.Count)
	return buf
}

func decodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderSize {
		return Header{}, fmt.Errorf("%w: %d of %d byte(s)", ErrHeaderIncomplete, len(buf), HeaderSize)
	}
	if string(buf[:4]) != magic {
		return Header{}, fmt.Errorf("segment: bad magic %q", buf[:4])
	}
	return Header{
		FirstSeq: binary.LittleEndian.Uint64(buf[8:]),
		Count:    binary.LittleEndian.Uint64(buf[16:]),
	}, nil
}

// ReadHeader 从文件头部读段头。
func ReadHeader(f *os.File) (Header, error) {
	buf := make([]byte, HeaderSize)
	n, err := f.ReadAt(buf, 0)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Header{}, fmt.Errorf("%w: %d of %d byte(s)", ErrHeaderIncomplete, n, HeaderSize)
		}
		return Header{}, err
	}
	return decodeHeader(buf)
}

// Writer 追加写单个段。OnEvent 非空时每追加一条回调 (seq, offset)。
type Writer struct {
	f       *os.File
	first   uint64
	count   uint64
	off     int64
	OnEvent func(seq, offset uint64)
}

// Create 新建段文件并写入初始段头（count=0，Close 时回填）。
func Create(path string, firstSeq uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(encodeHeader(Header{FirstSeq: firstSeq})); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, first: firstSeq, off: HeaderSize}, nil
}

// Append 追加一条事件，返回其序号。记录单次 Write 落盘。
func (w *Writer) Append(payload []byte) (uint64, error) {
	rec := event.Encode(payload)
	if _, err := w.f.Write(rec); err != nil {
		return 0, err
	}
	seq := w.first + w.count
	if w.OnEvent != nil {
		w.OnEvent(seq, uint64(w.off))
	}
	w.off += int64(len(rec))
	w.count++
	return seq, nil
}

// Count 返回已追加条数。
func (w *Writer) Count() uint64 { return w.count }

// Close 回填段头 count、落盘并关闭。
func (w *Writer) Close() error {
	if _, err := w.f.WriteAt(encodeHeader(Header{FirstSeq: w.first, Count: w.count}), 0); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	return w.f.Close()
}

// Scan 从 off 起顺序解码事件（size 为文件总字节数），逐条回调
// fn(seq, offset, payload)。干净结束在记录边界返回 nil；
// 解码失败把 event 包的错误原样包装返回。
func Scan(r io.ReaderAt, size int64, firstSeq uint64, off int64, fn func(seq, offset uint64, payload []byte)) error {
	pos := off
	for i := uint64(0); ; i++ {
		remain := size - pos
		if remain == 0 {
			return nil
		}
		if remain < event.LenSize {
			return fmt.Errorf("at offset %d: %w", pos, event.ErrLengthIncomplete)
		}
		var lenBuf [event.LenSize]byte
		if _, err := r.ReadAt(lenBuf[:], pos); err != nil {
			return err
		}
		payloadLen := int64(binary.LittleEndian.Uint32(lenBuf[:]))
		total := int64(event.EncodedSize(int(payloadLen)))
		n := total
		if n > remain {
			n = remain
		}
		rec := make([]byte, n)
		copy(rec, lenBuf[:])
		if _, err := r.ReadAt(rec[event.LenSize:], pos+event.LenSize); err != nil {
			return err
		}
		payload, _, err := event.Decode(rec)
		if err != nil {
			return fmt.Errorf("at offset %d: %w", pos, err)
		}
		fn(firstSeq+i, uint64(pos), payload)
		pos += total
	}
}

// ReadAll 读出段内全部可解码事件；遇损坏返回已读前缀与错误。
func ReadAll(path string) (Header, []event.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Header{}, nil, err
	}
	h, err := ReadHeader(f)
	if err != nil {
		return Header{}, nil, err
	}
	var evs []event.Event
	err = Scan(f, fi.Size(), h.FirstSeq, HeaderSize, func(seq, _ uint64, payload []byte) {
		evs = append(evs, event.Event{Seq: seq, Payload: payload})
	})
	return h, evs, err
}
