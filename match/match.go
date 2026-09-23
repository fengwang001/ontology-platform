package match

import "ontology/window"

// Matcher finds the longest hash-chain candidate in a window.
type Matcher struct {
	win *window.Window
}

func New(win *window.Window, maxChain int) *Matcher { return &Matcher{win: win} }
