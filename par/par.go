package par

import "ontology/stream"

type Result struct {
	Output []byte
	Stats  stream.Stats
	Checks int
}

func Transcode(input []byte, cfg stream.Config, k int) (Result, error) {
	return Result{}, nil
}
