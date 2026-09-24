package stream

import "ontology/u16"

type Encoding int

const (
	U8 Encoding = iota
	U16LE
	U16BE
	U16Auto
)

type Config struct {
	From, To      Encoding
	Strict        bool
	MaxOutput     int
	EmitInputBOM  bool
	EmitOutputBOM bool
	Offset        int64
}

type Stats struct {
	Consumed, Scalars, Invalid, InvalidBytes, BOMBytes, Output int64
	checks                                                     int64
}

type Transcoder struct {
	cfg Config
	out []byte
}

func New(cfg Config) *Transcoder { return &Transcoder{cfg: cfg} }

func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return append([]byte(nil), t.out...) }

func (t *Transcoder) Stats() Stats { return Stats{} }

func (t *Transcoder) Checks() int64 { return 0 }

func defaultOrder(e Encoding) u16.Order {
	if e == U16BE {
		return u16.BigEndian
	}
	return u16.LittleEndian
}
