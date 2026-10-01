package speculative

import "errors"
import "sync"

var (
	ErrInvalidBuildWindow = errors.New("speculative: build window must be at least 1")
	ErrInvalidCapacity    = errors.New("speculative: queue capacity must be at least 1")
	ErrEmptyID            = errors.New("speculative: id must not be empty")
	ErrAlreadyMerged      = errors.New("speculative: id has already been merged")
	ErrAlreadyQueued      = errors.New("speculative: id is already queued")
	ErrQueueFull          = errors.New("speculative: queue is full")
	ErrNotInQueue         = errors.New("speculative: id is not in the queue")
	ErrOutsideBuildWindow = errors.New("speculative: item is outside the active build window")
	ErrStaleEpoch         = errors.New("speculative: report epoch does not match the current epoch")
)

type Result struct {
	Merged  []string
	Removed string
	Bumped  []string
}

type StatusItem struct {
	ID       string
	Position int
	Epoch    int
	Building bool
}

type Coordinator struct {
	mu          sync.Mutex
	buildWindow int
	capacity    int
	items       []*queueItem
	merged      map[string]struct{}
	mergedOrder []string
}

type queueItem struct {
	id    string
	epoch int
}

func NewCoordinator(buildWindow int, capacity int) (*Coordinator, error) {
	if buildWindow < 1 {
		return nil, ErrInvalidBuildWindow
	}
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}

	return &Coordinator{
		buildWindow: buildWindow,
		capacity:    capacity,
		merged:      make(map[string]struct{}),
	}, nil
}

func (c *Coordinator) Enqueue(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if id == "" {
		return ErrEmptyID
	}
	if _, ok := c.merged[id]; ok {
		return ErrAlreadyMerged
	}
	for _, item := range c.items {
		if item.id == id {
			return ErrAlreadyQueued
		}
	}
	if len(c.items) >= c.capacity {
		return ErrQueueFull
	}

	c.items = append(c.items, &queueItem{id: id, epoch: 1})
	return nil
}

func (c *Coordinator) Report(id string, epoch int, passed bool) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	position := -1
	for index, item := range c.items {
		if item.id == id {
			position = index
			break
		}
	}
	if position == -1 {
		return Result{}, ErrNotInQueue
	}
	if position >= c.buildWindow {
		return Result{}, ErrOutsideBuildWindow
	}
	item := c.items[position]
	if item.epoch != epoch {
		return Result{}, ErrStaleEpoch
	}

	if passed {
		merging := make([]string, 0, position+1)
		for _, prefix := range c.items[:position+1] {
			merging = append(merging, prefix.id)
			c.merged[prefix.id] = struct{}{}
			c.mergedOrder = append(c.mergedOrder, prefix.id)
		}
		c.items = c.items[position+1:]
		return Result{Merged: merging, Bumped: []string{}}, nil
	}

	c.items = append(c.items[:position], c.items[position+1:]...)
	return Result{
		Merged:  []string{},
		Removed: id,
		Bumped:  c.bumpAfter(position),
	}, nil
}

func (c *Coordinator) Dequeue(id string) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	position := -1
	for index, item := range c.items {
		if item.id == id {
			position = index
			break
		}
	}
	if position == -1 {
		return Result{}, ErrNotInQueue
	}

	c.items = append(c.items[:position], c.items[position+1:]...)
	return Result{
		Merged:  []string{},
		Removed: id,
		Bumped:  c.bumpAfter(position),
	}, nil
}

func (c *Coordinator) Merged() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.mergedOrder...)
}

func (c *Coordinator) Status() []StatusItem {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := make([]StatusItem, len(c.items))
	for index, item := range c.items {
		status[index] = StatusItem{
			ID:       item.id,
			Position: index + 1,
			Epoch:    item.epoch,
			Building: index < c.buildWindow,
		}
	}
	return status
}

func (c *Coordinator) bumpAfter(position int) []string {
	bumped := make([]string, 0, len(c.items)-position)
	for index := position; index < len(c.items); index++ {
		c.items[index].epoch++
		bumped = append(bumped, c.items[index].id)
	}
	return bumped
}
