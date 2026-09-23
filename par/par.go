package par

import "ontology/stream"

type Result struct {
	Output []byte
	Stats  stream.Stats
	Err    error
}
