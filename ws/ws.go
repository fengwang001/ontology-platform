// Package ws performs delayed classification of trailing spaces and tabs:
// a run is known to be trailing only when a line ending or stream end arrives.
package ws

const (
	// Space is the ASCII space byte.
	Space = ' '
	// Tab is the ASCII tab byte.
	Tab = '\t'
)

// IsWS reports whether b is trailing-eligible whitespace (space or tab).
func IsWS(b byte) bool { return b == Space || b == Tab }

// TrailingRun returns the length of the trailing space/tab run in data.
func TrailingRun(data []byte) int {
	n := 0
	for n < len(data) && IsWS(data[len(data)-1-n]) {
		n++
	}
	return n
}

// Pending is a delayed-decision counter for a stream.
type Pending struct{ n int }

// Add records a whitespace byte; returns the new pending length.
func (p *Pending) Add() int { p.n++; return p.n }

// Observe records a non-whitespace byte, resolving the pending run.
func (p *Pending) Observe() { p.n = 0 }

// Len reports the current unresolved trailing run length.
func (p *Pending) Len() int { return p.n }

// Reset clears the pending run (a line ending resolves it as trailing).
func (p *Pending) Reset() { p.n = 0 }

// TrailingOffset walks back from cut over a trailing space/tab run; it does
// not cross a CR. It returns the safe cut offset.
func TrailingOffset(data []byte, cut int) int {
	for cut > 0 && IsWS(data[cut-1]) {
		cut--
	}
	return cut
}
