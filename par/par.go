package par

import "ontology/stream"

type Result struct {
	Output  []byte
	Stats   stream.Stats
	Checked int
}

func Transcode(input []byte, cfg stream.Config, workers int) (Result, error) {
	return Result{}, nil
}
