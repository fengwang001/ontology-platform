package split

import (
	"errors"

	"ontology/roll"
)

var (
	ErrMinMax      = errors.New("split: min must not exceed max")
	ErrLargeWindow = errors.New("split: rolling window must not exceed min")
	ErrRejected    = errors.New("split: chunk rejected")
)

type Chunk struct {
	Start int64
	End   int64
	Data  []byte
	Final bool
}

type Config struct {
	Min    int
	Max    int
	Window int
	Target uint32
}

type Splitter struct {
	cfg    Config
	offset int64
	start  int64
	buf    []byte
	hash   *roll.Hasher
}

func New(cfg Config) (*Splitter, error) {
	if cfg.Min > cfg.Max {
		return nil, ErrMinMax
	}
	if cfg.Window > cfg.Min {
		return nil, ErrLargeWindow
	}
	h, err := roll.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	return &Splitter{cfg: cfg, hash: h}, nil
}

func (s *Splitter) Write(p []byte, emit func(Chunk) bool) error {
	for _, b := range p {
		s.offset++
		s.buf = append(s.buf, b)
		length := int(s.offset - s.start)
		s.hash.Push(b)
		cut := false
		if length >= s.cfg.Min && s.hash.Full() && s.hash.Sum() == s.cfg.Target {
			cut = true
		}
		if length == s.cfg.Max {
			cut = true
		}
		if cut && !s.emit(emit, false) {
			return ErrRejected
		}
	}
	return nil
}

func (s *Splitter) Finish(emit func(Chunk) bool) error {
	if len(s.buf) == 0 {
		return nil
	}
	if !s.emit(emit, true) {
		return ErrRejected
	}
	return nil
}

func (s *Splitter) emit(emit func(Chunk) bool, final bool) bool {
	data := append([]byte(nil), s.buf...)
	c := Chunk{Start: s.start, End: s.offset, Data: data, Final: final}
	if !emit(c) {
		return false
	}
	s.start = s.offset
	s.buf = s.buf[:0]
	s.hash.Reset()
	return true
}

func (s *Splitter) Clone() *Splitter {
	h := *s.hash
	return &Splitter{cfg: s.cfg, offset: s.offset, start: s.start, buf: append([]byte(nil), s.buf...), hash: &h}
}
