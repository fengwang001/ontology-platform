package ontology

import "sync"

type Histogram struct {
	mu        sync.Mutex
	lo        float64
	hi        float64
	width     float64
	buckets   []uint64
	underflow uint64
	overflow  uint64
	skipped   uint64
	added     uint64
}
