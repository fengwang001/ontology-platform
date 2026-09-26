package match

import "ontology/window"

type Matcher struct {
	win *window.Ring
}

func New(win *window.Ring, maxChain int) (*Matcher, error) {
	return &Matcher{win: win}, nil
}

func (m *Matcher) LoadDictionary(p []byte) {}

func (m *Matcher) Candidates() uint64 { return 0 }

func (m *Matcher) Find(data []byte, pos int) (distance, length int) {
	return 0, 0
}
