package par

import (
	"ontology/stream"
	"ontology/u8"
)

func Transcode(in []byte, k int, output int, emitBOM bool) ([]byte, stream.Stats, error) {
	return nil, stream.Stats{}, nil
}

var _ = u8.Boundary
