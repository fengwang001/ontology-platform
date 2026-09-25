package resume

import (
	"fmt"
	"time"

	"ontology/chunker"
)

// State is a resumable checkpoint of pipeline internals.
type State struct {
	Queue     []chunker.Chunk
	FrontOff  int
	Pend      []byte
	Start     time.Time
	Open      bool
	Final     bool
	Closed    bool
	Accepted  int64
	Confirmed int64
	Produced  int64
	Sizes     []int
}

// Validate checks checkpoint consistency, including the
// accepted = confirmed + buffered identity (all in source bytes).
func (s State) Validate() error {
	if s.Accepted < 0 || s.Confirmed < 0 || s.Produced < 0 || s.FrontOff < 0 {
		return fmt.Errorf("resume: negative field in state")
	}
	if s.Accepted < s.Confirmed {
		return fmt.Errorf("resume: accepted %d < confirmed %d", s.Accepted, s.Confirmed)
	}
	var buffered int64 = int64(len(s.Pend))
	for _, q := range s.Queue {
		if q.Len() == 0 {
			return fmt.Errorf("resume: empty chunk in queue")
		}
		buffered += int64(q.Len())
	}
	if s.Accepted-s.Confirmed != buffered {
		return fmt.Errorf("resume: identity broken: %d-%d != %d",
			s.Accepted, s.Confirmed, buffered)
	}
	if !s.Open && len(s.Pend) > 0 {
		return fmt.Errorf("resume: pending bytes without open group")
	}
	return nil
}
