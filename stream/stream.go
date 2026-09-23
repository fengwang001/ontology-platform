package stream

import "ontology/u16"

const (
	UTF8     = 0
	UTF16LE  = u16.LE
	UTF16BE  = u16.BE
	NoLimit  = 0
	Replace  = 0
	Strict   = 1
)

type Stats struct {
	Valid       int
	Invalid     int
	InvalidBytes int
	BOMBytes    int
	Consumed    int
	Checks      int
}

type Config struct {
	Input       int
	Output      int
	Strict      bool
	EmitBOM     bool
	MaxOutput   int
}

type Transcoder struct{}

func New(c Config) *Transcoder { return nil }

func (t *Transcoder) Write(p []byte) (int, error) { return 0, nil }

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return nil }

func (t *Transcoder) Pending() []byte { return nil }

func (t *Transcoder) Stats() Stats { return Stats{} }
