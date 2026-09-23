package match

import (
	"errors"

	"ontology/window"
)

const (
	MinMatch = 4
	MaxMatch = 258
)

var ErrIllegalConfig = errors.New("match: chain limit must be positive")

type Matcher struct {
	win        *window.Window
	chainLimit int
	examined   int64
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrIllegalConfig
	}
	return &Matcher{win: win, chainLimit: chainLimit}, nil
}

func (m *Matcher) Examined() int64 { return m.examined }

func (m *Matcher) ResetCandidates() { m.examined = 0 }

func (m *Matcher) Preset(data []byte) {}

func (m *Matcher) Search(data []byte, pos, lookahead int) (distance, length int) {
	return 0, 0
}

func (m *Matcher) Advance(b byte) {
	m.win.Add(b)
}
