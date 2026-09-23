package stream

import (
	"errors"

	"ontology/u16"
	"ontology/u8"
)

type Format uint8

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
)

type Config struct {
	From, To     Format
	Strict       bool
	KeepBOM      bool
	OutputLimit  int
}

type Stats struct {
	ValidScalars int64
	InvalidUnits int64
	InvalidBytes int64
	BOMBytes     int64
	Consumed     int64
}

type Transcoder struct{}

var (
	ErrInvalid     = errors.New("invalid encoded unit")
	ErrTruncated   = errors.New("truncated input")
	ErrLimit       = errors.New("output limit exceeded")
	ErrClosed      = errors.New("transcoder closed")
	ErrTerminal    = errors.New("transcoder in terminal state")
)

type RangeError struct{ Offset, Size int64 }

func New(c Config) *Transcoder { return &Transcoder{} }

func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return nil }

func (t *Transcoder) Pending() []byte { return nil }

func (t *Transcoder) Stats() Stats { return Stats{} }

func (t *Transcoder) Checks() int64 { return 0 }

var (
	_ = u8.NewDecoder
	_ = u16.NewDecoder
)
