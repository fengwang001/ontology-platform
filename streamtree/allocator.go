package streamtree

import (
	"errors"
	"io"
	"sync"
)

const RootID = 0

var (
	ErrInvalidID           = errors.New("stream id must be a positive unused integer")
	ErrStreamNotFound      = errors.New("stream not found")
	ErrParentNotFound      = errors.New("parent stream not found")
	ErrSelfParent          = errors.New("new parent cannot be the stream itself")
	ErrInvalidWeight       = errors.New("weight must be between 1 and 256")
	ErrNegativeQuota       = errors.New("quota must not be negative")
	ErrReadyStreamNotFound = errors.New("ready stream not found")
)

type stream struct {
	id     int
	parent int
	weight int
}

type Allocator struct {
	mu       sync.RWMutex
	logMu    sync.Mutex
	streams  map[int]*stream
	children map[int]map[int]struct{}
	w        io.Writer
}

func New(w io.Writer) *Allocator {
	return &Allocator{
		streams:  map[int]*stream{},
		children: map[int]map[int]struct{}{RootID: {}},
		w:        w,
	}
}

func (a *Allocator) Open(id, parent, weight int, exclusive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id <= 0 || a.streams[id] != nil {
		return a.reject("open", id, parent, weight, exclusive, ErrInvalidID)
	}
	if !a.alive(parent) {
		return a.reject("open", id, parent, weight, exclusive, ErrParentNotFound)
	}
	if weight < 1 || weight > 256 {
		return a.reject("open", id, parent, weight, exclusive, ErrInvalidWeight)
	}

	a.streams[id] = &stream{id: id, parent: parent, weight: weight}
	a.children[id] = map[int]struct{}{}
	a.attach(id, parent, exclusive)
	a.log("open", id, parent, weight, exclusive, nil, "accepted: stream attached")
	return nil
}

func (a *Allocator) Reset(id, newParent, newWeight int, exclusive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	node := a.streams[id]
	if node == nil {
		return a.reject("reset", id, newParent, newWeight, exclusive, ErrStreamNotFound)
	}
	if !a.alive(newParent) {
		return a.reject("reset", id, newParent, newWeight, exclusive, ErrParentNotFound)
	}
	if newParent == id {
		return a.reject("reset", id, newParent, newWeight, exclusive, ErrSelfParent)
	}
	if newWeight < 1 || newWeight > 256 {
		return a.reject("reset", id, newParent, newWeight, exclusive, ErrInvalidWeight)
	}

	oldParent := node.parent
	if a.isDescendant(id, newParent) {
		a.moveSubtree(newParent, oldParent)
	}
	a.detach(id)
	a.attach(id, newParent, exclusive)
	node.weight = newWeight
	a.log("reset", id, newParent, newWeight, exclusive, nil, "accepted: dependency reset")
	return nil
}

func (a *Allocator) Close(id int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	node := a.streams[id]
	if node == nil {
		return a.reject("close", id, 0, 0, false, ErrStreamNotFound)
	}

	parent := node.parent
	kids := sortedKeys(a.children[id])
	totalWeight := 0
	for _, child := range kids {
		totalWeight += a.streams[child].weight
	}

	for _, child := range kids {
		childNode := a.streams[child]
		if totalWeight > 0 {
			newWeight := node.weight * childNode.weight / totalWeight
			if newWeight < 1 {
				newWeight = 1
			}
			childNode.weight = newWeight
		}
		a.moveSubtree(child, parent)
	}

	delete(a.streams, id)
	delete(a.children[parent], id)
	delete(a.children, id)
	a.log("close", id, parent, node.weight, false, nil, "accepted: children moved and weights redistributed")
	return nil
}
