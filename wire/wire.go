package wire

import "errors"

const (
	Magic0 byte = 0x4F
	Magic1 byte = 0x4E
	Version byte = 1

	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

var (
	ErrVarintLong = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOver = errors.New("wire: varint overflows uint64")
	ErrTruncated  = errors.New("wire: truncated stream")
)

type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return "" }
func (e *Error) Unwrap() error { return nil }

func AppendHeader(dst []byte) []byte { return dst }
func AppendTag(dst []byte, t byte) []byte { return dst }
func AppendUvarint(dst []byte, v uint64) []byte { return dst }
func AppendLiteral(dst, lit []byte) []byte { return dst }
func AppendMatch(dst []byte, dist, length int) []byte { return dst }
func AppendEnd(dst []byte, total int, checksum uint32) []byte { return dst }

type Reader struct{}

func NewReader() *Reader { return nil }
func (r *Reader) Offset() int { return 0 }
func (r *Reader) Feed(b []byte) bool { return false }
func (r *Reader) ExpectHeader() error { return nil }
func (r *Reader) Tag() (byte, error) { return 0, nil }
func (r *Reader) Uvarint() (uint64, error) { return 0, nil }
func (r *Reader) Bytes(n int) ([]byte, error) { return nil, nil }
