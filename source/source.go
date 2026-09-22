// Package source defines the in-process byte source that a Range response is
// assembled from. A Source may legally return short reads; its length may
// change between calls. Implementations are safe for concurrent use by
// independent readers.
package source

// Source serves bytes of a mutable, in-memory representation.
//
// ReadAt follows io.ReaderAt semantics loosely: it returns n bytes copied
// into p (0 <= n <= len(p)). A short read (n < len(p), err == nil) is legal
// and the caller must keep reading. Reading past the current end returns
// the bytes available; a read that starts past the end returns n==0.
type Source interface {
	// Size returns the current representation length in bytes.
	Size() int64
	// ReadAt copies up to len(p) bytes starting at off.
	ReadAt(p []byte, off int64) (n int, err error)
}

// ErrUnexpectedEOFLength is returned by ReadFull when a Source ends before
// the requested number of bytes could be obtained. It is a distinct,
// detectable error: callers must not silently truncate the body.
type ErrUnexpectedEOFLength struct {
	Wanted int64
	Got    int64
}

func (e *ErrUnexpectedEOFLength) Error() string {
	return "source: unexpected end: wanted " + itoa(e.Wanted) + " bytes, got " + itoa(e.Got)
}

// ReadFull reads exactly len(buf) bytes from src starting at off, looping
// over short reads. It returns the number of bytes obtained alongside
// *ErrUnexpectedEOFLength when the source ends early.
func ReadFull(src Source, buf []byte, off int64) (int, error) {
	return 0, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
