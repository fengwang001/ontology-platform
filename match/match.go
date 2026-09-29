package match

import (
	"errors"

	"ontology/window"
)

var ErrChainLimit = errors.New("match: chain limit must be positive")

const MinLength = 3

type Matcher struct{}

func New(win *window.Window, chainLimit int) (*Matcher, error) { return nil, nil }
func (m *Matcher) Candidates() int64 { return 0 }
func (m *Matcher) Feed(data []byte, pos int) {}
func (m *Matcher) Find(data []byte, pos int) (dist, length int) { return 0, 0 }
