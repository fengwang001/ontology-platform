package stream

import "ontology/u16"

type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

type Stats struct {
	Scalars int64
	Invalid int64
	BadByte int64
	BOMByte int64
	Checks  int64
}

type Config struct {
	From       Encoding
	To         Encoding
	Strict     bool
	KeepBOM    bool
	MaxOutput  int
}

type Transcoder struct{}

func New(c Config) *Transcoder { return &Transcoder{} }

func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return nil }

func (t *Transcoder) Pending() []byte { return nil }

func (t *Transcoder) Stats() Stats { return Stats{} }

var _ = u16.LE
