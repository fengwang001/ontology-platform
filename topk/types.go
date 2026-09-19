package topk

import "sync"

// Direction controls which score dimension ranks elements.
type Direction int

const (
	// Desc keeps the K largest scores.
	Desc Direction = iota
	// Asc keeps the K smallest scores.
	Asc
)

// Element is a ranked (ID, Score) pair.
type Element struct {
	ID    string
	Score float64
}

// Selector maintains at most K elements under a fixed ranking order.
// The zero value is not usable; construct it with New.
type Selector struct {
	mu      sync.RWMutex
	k       int
	dir     Direction
	score   map[string]float64
	held    idHeap
	skipped int64
}
