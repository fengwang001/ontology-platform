package splitdeque

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("split deque: invalid configuration")
	ErrInvalidArgument = errors.New("split deque: invalid argument")
	ErrFull            = errors.New("split deque: full")
)

type Stats struct {
	Pushed       int64
	Popped       int64
	Stolen       int64
	FailedSteals int64
	Releases     int64
	Reclaims     int64
}

type SplitDeque struct {
	mu        sync.Mutex
	data      []int64
	capacity  int
	sharedMax int
	reserve   int
	freshness int

	top    int64
	split  int64
	bottom int64

	requested bool
	age       int64
	missing   int64

	stats          Stats
	movedElements  int64
	copiedElements int64
}

func New(capacity, sharedLimit, privateReserve, freshness int) (*SplitDeque, error) {
	if capacity < 1 || capacity > 1_000_000 ||
		sharedLimit < 1 || sharedLimit > capacity ||
		privateReserve < 0 || privateReserve > capacity ||
		freshness < 1 || freshness > 1_000_000 {
		return nil, ErrInvalidConfig
	}

	return &SplitDeque{
		data:      make([]int64, capacity),
		capacity:  capacity,
		sharedMax: sharedLimit,
		reserve:   privateReserve,
		freshness: freshness,
	}, nil
}

func (d *SplitDeque) Push(value int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.bottom-d.top == int64(d.capacity) {
		return ErrFull
	}

	d.data[d.bottom%int64(d.capacity)] = value
	d.bottom++
	d.stats.Pushed++
	d.releaseLocked()
	return nil
}

func (d *SplitDeque) Pop() (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.releaseLocked()

	if d.bottom > d.split {
		d.bottom--
		value := d.data[d.bottom%int64(d.capacity)]
		d.stats.Popped++
		return value, true
	}

	if d.split == d.top {
		return 0, false
	}

	shared := d.split - d.top
	giveBack := (shared + 1) / 2
	d.split -= giveBack
	d.stats.Reclaims++

	d.bottom--
	value := d.data[d.bottom%int64(d.capacity)]
	d.stats.Popped++
	return value, true
}

func (d *SplitDeque) Steal(count int) ([]int64, error) {
	if count < 1 || count > d.capacity {
		return nil, ErrInvalidArgument
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	shared := d.split - d.top
	taken := int64(count)
	if taken > shared {
		taken = shared
	}

	values := make([]int64, 0, taken)
	for i := d.top; i < d.top+taken; i++ {
		values = append(values, d.data[i%int64(d.capacity)])
		d.copiedElements++
	}
	d.top += taken
	d.stats.Stolen += taken

	if taken < int64(count) {
		missing := int64(count) - taken
		if d.requested && missing > d.missing {
			d.missing = missing
		} else if !d.requested {
			d.missing = missing
		}
		d.requested = true
		d.age = 0
		d.stats.FailedSteals++
	}

	return values, nil
}

func (d *SplitDeque) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

func (d *SplitDeque) releaseLocked() {
	if !d.requested {
		return
	}

	privateCount := d.bottom - d.split
	sharedCount := d.split - d.top
	desired := privateCount / 2
	if d.missing > desired {
		desired = d.missing
	}
	sharedRoom := int64(d.sharedMax) - sharedCount
	privateSurplus := privateCount - int64(d.reserve)
	releaseCount := desired
	if sharedRoom < releaseCount {
		releaseCount = sharedRoom
	}
	if privateSurplus < releaseCount {
		releaseCount = privateSurplus
	}
	if releaseCount < 0 {
		releaseCount = 0
	}

	if releaseCount >= 1 {
		d.split += releaseCount
		d.requested = false
		d.age = 0
		d.missing = 0
		d.stats.Releases++
		return
	}

	d.age++
	if d.age == int64(d.freshness) {
		d.requested = false
		d.age = 0
		d.missing = 0
	}
}
