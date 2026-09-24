package rle

import (
	"errors"
	"unicode/utf8"

	"ontology/runs"
)

var (
	ErrCountOne      = errors.New("rle: explicit repeat count 1")
	ErrCountZero     = errors.New("rle: repeat count 0")
	ErrLeadingZero   = errors.New("rle: leading zero in repeat count")
	ErrAdjacentRuns  = errors.New("rle: adjacent runs have the same symbol")
	ErrInvalidEscape = errors.New("rle: backslash must escape a digit or backslash")
	ErrTrailingSlash = errors.New("rle: trailing backslash")
	ErrMissingSymbol = errors.New("rle: repeat count is not followed by a symbol")
	ErrInvalidUTF8   = errors.New("rle: invalid UTF-8")
	ErrOutputLimit   = errors.New("rle: decoded output exceeds limit")
)

type DecodeError struct {
	Op     error
	Offset int64
}

func (e *DecodeError) Error() string { return e.Op.Error() }
func (e *DecodeError) Unwrap() error { return e.Op }

func Encode(s string) string {
	encoded := make([]byte, 0, len(s))
	for _, run := range runs.Split(s) {
		encoded = runs.AppendCount(encoded, run.Count)
		if (run.Symbol >= '0' && run.Symbol <= '9') || run.Symbol == '\\' {
			encoded = append(encoded, '\\', byte(run.Symbol))
		} else {
			encoded = utf8.AppendRune(encoded, run.Symbol)
		}
	}
	return string(encoded)
}

func Decode(t string) (string, error) { return t, nil }

type Decoder struct {
	output       interface{ Write([]byte) (int, error) }
	limit        int64
	bytesChecked int64
	err          error
}

func NewDecoder(output interface {
	Write([]byte) (int, error)
}, limit int64) *Decoder {
	return &Decoder{output: output, limit: limit}
}

func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }
func (d *Decoder) Close() error                { return nil }
func (d *Decoder) BytesChecked() int64         { return d.bytesChecked }
