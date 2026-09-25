package par

import "ontology/stream"

type Result struct {
	Output []byte
	Stats  stream.Stats
}

func Transcode(data []byte, c stream.Config, k int) (Result, error) {
	return Result{}, nil
}
