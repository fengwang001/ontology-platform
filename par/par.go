// Package par transcodes one large input using K goroutines while
// producing byte-identical output to a single stream.Transcoder.
package par

import (
	"ontology/stream"
	"ontology/u8"
)

// Result is one parallel transcode run.
type Result struct {
	Output []byte
	Stats  stream.Stats
	Err    error
}

// Run splits input into k arbitrary byte ranges and transcodes them
// concurrently using cfg. k must be in 1..8.
func Run(input []byte, cfg stream.Config, k int) Result {
	_ = u8.Rune
	return Result{}
}
