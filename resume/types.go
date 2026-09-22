package resume

import "ontology/chunker"

type Chunk struct {
	Payload    []byte
	Extensions []chunker.Extension
}

type State struct{}

type Store struct{}
