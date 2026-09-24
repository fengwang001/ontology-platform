package par

import "ontology/stream"

type Result struct {
	Output []byte
	Stats  stream.Stats
}

func UTF8ToUTF8(input []byte, k int) Result { return Result{} }
