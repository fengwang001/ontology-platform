package match

import (
	"errors"

	"ontology/window"
)

const MinLength = 3

var (
	ErrInvalidChainLimit = errors.New("match: chain limit must be positive")
	ErrInvalidWindow     = errors.New("match: nil window")
)

type Result struct {
	Distance int
	Length   int
}

type Matcher struct {
	win         *window.Window
	chainLimit  int
	chains      []int
	head        []int
	nextEmit    int
	examined    int
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	return &Matcher{}, nil
}

func (m *Matcher) Reset(win *window.Window) {}

func (m *Matcher) AddDictionary(data []byte) {}

func (m *Matcher) Find(data []byte, maxLength int) Result { return Result{} }

func (m *Matcher) Insert(data []byte) {}

func (m *Matcher) CandidatesExamined() int { return 0 }
