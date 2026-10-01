// Package waterfall implements a layered waterfall distributor with clawback.
package waterfall

import (
	"errors"
	"sync"
)

// MaxTotal is the inclusive upper bound of the total allocated amount
// (cumulative allocation minus cumulative clawback).
const MaxTotal = 1_000_000_000_000_000

var (
	// ErrInvalidConfig means fewer than 2 layers, a negative cap or an
	// out-of-range manager percentage.
	ErrInvalidConfig = errors.New("waterfall: invalid config")
	// ErrNonPositiveAmount means the allocation or clawback amount is <= 0.
	ErrNonPositiveAmount = errors.New("waterfall: amount must be positive")
	// ErrClawbackExceedsReceived means a clawback asks for more than the
	// currently received total.
	ErrClawbackExceedsReceived = errors.New("waterfall: clawback exceeds total received")
	// ErrTotalExceedsLimit means the allocation would push the total above
	// MaxTotal.
	ErrTotalExceedsLimit = errors.New("waterfall: total would exceed 1e15")
)

// Layer is one level of the waterfall. Every layer except the last has a
// cumulative cap; the last layer is the uncapped remainder layer and carries
// the manager percentage g (0..100).
type Layer struct {
	Cap  int64
	G    int
	Recv int64
}

// Splits is the cumulative split of the remainder (last) layer.
type Splits struct {
	Received int64
	Manager  int64
	Investor int64
}

// State is a point-in-time snapshot of every layer.
type State struct {
	Layers []Layer
	Split  Splits
}

// Water is a concurrency-safe layered waterfall distributor.
type Water struct {
	mu     sync.Mutex
	layers []Layer
}

// New validates caps/g and returns an empty distributor.
func New(layers []Layer) (*Water, error) {
	if len(layers) < 2 {
		return nil, ErrInvalidConfig
	}
	for i, layer := range layers {
		if layer.Cap < 0 {
			return nil, ErrInvalidConfig
		}
		if i == len(layers)-1 && (layer.G < 0 || layer.G > 100) {
			return nil, ErrInvalidConfig
		}
		if layer.Recv != 0 {
			return nil, ErrInvalidConfig
		}
	}
	copied := make([]Layer, len(layers))
	copy(copied, layers)
	return &Water{layers: copied}, nil
}

// Allocate distributes x across the layers from layer 0.
func (w *Water) Allocate(x int64) (*State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if x <= 0 {
		return nil, ErrNonPositiveAmount
	}
	total := sumRecv(w.layers)
	if x > MaxTotal-total {
		return nil, ErrTotalExceedsLimit
	}

	remaining := x
	for i := range w.layers {
		if remaining == 0 {
			break
		}
		if i == len(w.layers)-1 {
			w.layers[i].Recv += remaining
			remaining = 0
			break
		}
		room := w.layers[i].Cap - w.layers[i].Recv
		fill := remaining
		if fill > room {
			fill = room
		}
		w.layers[i].Recv += fill
		remaining -= fill
	}
	return w.snapshotLocked(), nil
}

// Clawback reverses y of previously allocated amounts, last layer first.
func (w *Water) Clawback(y int64) (*State, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if y <= 0 {
		return nil, ErrNonPositiveAmount
	}
	total := sumRecv(w.layers)
	if y > total {
		return nil, ErrClawbackExceedsReceived
	}

	remaining := y
	for i := len(w.layers) - 1; i >= 0; i-- {
		if remaining == 0 {
			break
		}
		cut := remaining
		if cut > w.layers[i].Recv {
			cut = w.layers[i].Recv
		}
		w.layers[i].Recv -= cut
		remaining -= cut
	}
	return w.snapshotLocked(), nil
}

// Query returns a consistent snapshot of the current state.
func (w *Water) Query() *State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snapshotLocked()
}

func sumRecv(layers []Layer) int64 {
	var total int64
	for _, layer := range layers {
		total += layer.Recv
	}
	return total
}

func (w *Water) snapshotLocked() *State {
	layers := make([]Layer, len(w.layers))
	copy(layers, w.layers)
	last := layers[len(layers)-1]
	manager := last.Recv * int64(last.G) / 100
	return &State{
		Layers: layers,
		Split: Splits{
			Received: last.Recv,
			Manager:  manager,
			Investor: last.Recv - manager,
		},
	}
}
