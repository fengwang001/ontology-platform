package segment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/posting"
)

// Truncation / corruption classes, distinguishable with errors.Is.
var (
	ErrHeaderIncomplete   = errors.New("segment: header incomplete")
	ErrDictIncomplete     = errors.New("segment: dictionary incomplete")
	ErrPostingsIncomplete = errors.New("segment: postings incomplete")
	ErrCRCMismatch        = errors.New("segment: crc mismatch")
)

// Open strictly reads a segment file, classifying any failure into one of
// the four error classes above.
func Open(path string) (*Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data)
}

// Recover reads the maximal recoverable prefix: every term kept in the
// dictionary has a complete, readable posting list; incomplete trailing
// records are dropped. It returns the partial segment plus the classified
// error (nil when the file is fully valid).
func Recover(path string) (*Segment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seg, err := parse(data)
	if seg != nil {
		return seg, err
	}
	return &Segment{lists: map[string]posting.List{}}, err
}

// parse decodes data. On failure it returns the partial segment parsed so
// far (nil if the header is unreadable) and the classified error.
func parse(data []byte) (*Segment, error) {
	if len(data) < HeaderSize || !bytes.Equal(data[:8], magic) ||
		binary.LittleEndian.Uint32(data[8:]) != version {
		return nil, ErrHeaderIncomplete
	}
	termCount := binary.LittleEndian.Uint32(data[12:])
	bodyLen := binary.LittleEndian.Uint64(data[16:])

	seg := &Segment{lists: map[string]posting.List{}}
	body := data[HeaderSize:]
	readUvarint := func() (uint64, bool) {
		v, n := binary.Uvarint(body)
		if n <= 0 {
			return 0, false
		}
		body = body[n:]
		return v, true
	}
	for i := uint32(0); i < termCount; i++ {
		termLen, ok := readUvarint()
		if !ok || uint64(len(body)) < termLen {
			return seg, ErrDictIncomplete
		}
		term := string(body[:termLen])
		body = body[termLen:]
		postLen, ok := readUvarint()
		if !ok || uint64(len(body)) < postLen {
			return seg, ErrPostingsIncomplete
		}
		list, err := posting.Decode(body[:postLen])
		if err != nil {
			return seg, fmt.Errorf("%w: %v", ErrPostingsIncomplete, err)
		}
		body = body[postLen:]
		seg.terms = append(seg.terms, term)
		seg.lists[term] = list
	}
	consumed := uint64(len(data) - HeaderSize - len(body))
	if consumed != bodyLen || len(body) < 4 {
		return seg, ErrCRCMismatch
	}
	want := binary.LittleEndian.Uint32(body[:4])
	got := crc32.ChecksumIEEE(data[:HeaderSize+bodyLen])
	if want != got {
		return seg, ErrCRCMismatch
	}
	return seg, nil
}
