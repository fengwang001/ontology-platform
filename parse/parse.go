// Package parse turns raw byte lines into records.
package parse

import (
	"errors"
	"strconv"
	"strings"
)

// Record is one successfully parsed input record.
type Record struct {
	Offset int64
	Key    string
	Val    int64
}

// ErrBad marks an unparseable record.
var ErrBad = errors.New("parse: bad record")

// Parse decodes one raw line. A line must contain a single '=': the part
// before it is the group key (empty string is legal), the part after it must
// be a non-negative integer. A missing '=' or bad value yields ErrBad.
func Parse(offset int64, data []byte) (Record, error) {
	line := strings.TrimRight(string(data), "\r\n")
	key, val, ok := strings.Cut(line, "=")
	if !ok {
		return Record{}, ErrBad
	}
	if strings.ContainsRune(val, '=') {
		return Record{}, ErrBad
	}
	v, err := strconv.ParseInt(val, 10, 64)
	if err != nil || v < 0 {
		return Record{}, ErrBad
	}
	return Record{Offset: offset, Key: key, Val: v}, nil
}
