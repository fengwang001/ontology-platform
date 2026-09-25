package journal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"

	"ontology/change"
)

const (
	magic      = "ONTJNL02"
	HeaderSize = len(magic)
)

var (
	ErrIncompleteHeader = errors.New("journal header incomplete")
	ErrIncompleteLength = errors.New("journal length prefix incomplete")
	ErrIncompleteRecord = errors.New("journal record incomplete")
	ErrCRC              = errors.New("journal CRC mismatch")
	ErrBadHeader        = errors.New("journal header mismatch")
)

type Journal struct{ file *os.File }

func Create(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(magic); err != nil || f.Sync() != nil {
		f.Close()
		return nil, err
	}
	return &Journal{file: f}, nil
}

func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	_, end, err := ScanFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if end >= 0 {
		if err := f.Truncate(end); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{file: f}, nil
}

func frame(c change.Change, committed byte) ([]byte, error) {
	body, err := c.Encode()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 13+len(body))
	var h [8]byte
	binary.BigEndian.PutUint32(h[:4], uint32(len(body)))
	binary.BigEndian.PutUint32(h[4:], crc32.ChecksumIEEE(body))
	buf = append(buf, h[:]...)
	buf = append(buf, body...)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(buf))
	return append(append(buf, crc[:]...), committed), nil
}

func (j *Journal) Append(c change.Change) error {
	buf, err := frame(c, 1)
	if err != nil {
		return err
	}
	if _, err := j.file.Write(buf); err != nil {
		return err
	}
	return j.file.Sync()
}

func (j *Journal) AppendPending(c change.Change) error {
	buf, err := frame(c, 0)
	if err != nil {
		return err
	}
	if _, err := j.file.Write(buf); err != nil {
		return err
	}
	return j.file.Sync()
}

func (j *Journal) MarkCommit() error {
	pos, err := j.file.Seek(-1, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := j.file.WriteAt([]byte{1}, pos); err != nil {
		return err
	}
	return j.file.Sync()
}

func (j *Journal) DiscardPending() error {
	pos, err := j.file.Seek(-1, io.SeekEnd)
	if err != nil {
		return err
	}
	return j.file.Truncate(pos)
}

func (j *Journal) Close() error { return j.file.Close() }

func Replay(path string) ([]change.Change, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cs, _, err := Scan(data)
	return cs, err
}

func Parse(data []byte) ([]change.Change, error) {
	cs, _, err := Scan(data)
	return cs, err
}

func ScanFile(f *os.File) ([]change.Change, int64, error) {
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, -1, err
	}
	return Scan(data)
}

func Scan(data []byte) ([]change.Change, int64, error) {
	if len(data) < len(magic) {
		return nil, -1, ErrIncompleteHeader
	}
	if string(data[:len(magic)]) != magic {
		return nil, -1, ErrBadHeader
	}
	out := []change.Change{}
	for pos := len(magic); pos < len(data); {
		if len(data)-pos < 4 {
			return out, -1, ErrIncompleteLength
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if len(data)-pos < 12+n {
			return out, -1, ErrIncompleteRecord
		}
		end := pos + 12 + n
		bodyCRC := binary.BigEndian.Uint32(data[pos+4 : pos+8])
		frameCRC := binary.BigEndian.Uint32(data[end-4 : end])
		if crc32.ChecksumIEEE(data[pos:end-4]) != frameCRC ||
			crc32.ChecksumIEEE(data[pos+8:end-4]) != bodyCRC {
			return out, -1, ErrCRC
		}
		c, err := change.Decode(data[pos+8 : end-4])
		if err != nil {
			return out, -1, err
		}
		if len(data) < end+1 {
			return out, int64(pos), nil
		}
		if data[end] == 0 {
			return out, int64(pos), nil
		}
		out = append(out, c)
		pos = end + 1
	}
	return out, -1, nil
}

func ClassifyTruncation(data, full []byte) error {
	_, _, err := Scan(data)
	if err == nil && len(data) < len(full) {
		return ErrIncompleteLength
	}
	return err
}
