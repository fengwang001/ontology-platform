package sink

import (
	"errors"
	"io"
)

var ErrWouldBlock = errors.New("sink temporarily unavailable")

type Sink = io.Writer

type Recorder struct{}

func (r *Recorder) Write(p []byte) (int, error) { return 0, nil }
