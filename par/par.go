package par

import (
	"ontology/stream"
)

type Result struct {
	Output []byte
	Stats  stream.Stats
	Err    error
}

func Transcode(data []byte, cfg stream.Config, k int) Result { return Result{} }
