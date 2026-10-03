package flexray

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("flexray: invalid argument")
	ErrInvalidID       = errors.New("flexray: frame id out of range")
	ErrDuplicateID     = errors.New("flexray: frame id already assigned")
	ErrNotAssigned     = errors.New("flexray: frame id not assigned")
	ErrQueueFull       = errors.New("flexray: dynamic queue full")
)

type SlotTag = any

type postedMessage struct {
	length int
	tag    SlotTag
}

type frame struct {
	base   int
	rep    int
	static bool
	buffer *postedMessage
	queue  []postedMessage
}

type SentItem struct {
	ID    int
	Tag   SlotTag
	Slot  int
	Start int
}

type CycleResult struct {
	Cycle       int
	Sent        []SentItem
	UnusedSlots int
	EmptyFrames int
}

type StatsResult struct {
	Sent       int
	Empty      int
	Overwrites int
}

type Arbiter struct {
	mu              sync.Mutex
	ns              int
	nm              int
	lt              int
	cycle           int
	frames          []*frame
	totalSent       int
	totalEmpty      int
	totalOverwrites int
}

func (f *frame) active(cycle int) bool {
	return f != nil && cycle%f.rep == f.base
}

func New(ns int, nm int, lt int) (*Arbiter, error) {
	if ns < 1 || ns > 64 || nm < 0 || nm > 256 || lt < 0 || lt > nm {
		return nil, ErrInvalidArgument
	}

	return &Arbiter{
		ns:     ns,
		nm:     nm,
		lt:     lt,
		frames: make([]*frame, ns+nm+1),
	}, nil
}

func validRepetition(rep int) bool {
	switch rep {
	case 1, 2, 4, 8, 16, 32, 64:
		return true
	default:
		return false
	}
}

func (a *Arbiter) Assign(id int, base int, rep int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !validRepetition(rep) || base < 0 || base >= rep {
		return ErrInvalidArgument
	}
	if id < 1 || id > a.ns+a.nm {
		return ErrInvalidID
	}
	if a.frames[id] != nil {
		return ErrDuplicateID
	}

	a.frames[id] = &frame{
		base:   base,
		rep:    rep,
		static: id <= a.ns,
	}

	return nil
}

func (a *Arbiter) Post(id int, length int, tag SlotTag) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id > a.ns && (length < 1 || length > a.nm) {
		return ErrInvalidArgument
	}
	if id < 1 || id > a.ns+a.nm {
		return ErrInvalidID
	}

	target := a.frames[id]
	if target == nil {
		return ErrNotAssigned
	}

	message := postedMessage{length: length, tag: tag}
	if target.static {
		if target.buffer != nil {
			a.totalOverwrites++
		}
		target.buffer = &message
		return nil
	}

	if length < 1 || length > a.nm {
		return ErrInvalidArgument
	}
	if len(target.queue) == 8 {
		return ErrQueueFull
	}
	target.queue = append(target.queue, message)

	return nil
}

func (a *Arbiter) Cycle() CycleResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	currentCycle := a.cycle
	result := CycleResult{Cycle: currentCycle}

	for id := 1; id <= a.ns; id++ {
		target := a.frames[id]
		if !target.active(currentCycle) {
			continue
		}
		if target.buffer == nil {
			result.UnusedSlots++
			result.EmptyFrames++
			a.totalEmpty++
			continue
		}

		result.Sent = append(result.Sent, SentItem{
			ID:   id,
			Tag:  target.buffer.tag,
			Slot: id,
		})
		target.buffer = nil
		a.totalSent++
	}

	dynamicSlots := a.nm + result.UnusedSlots
	k := a.ns + 1
	for i := 1; i <= dynamicSlots; {
		if k > a.ns+a.nm {
			i++
			k++
			continue
		}

		target := a.frames[k]
		if target.active(currentCycle) && len(target.queue) > 0 {
			message := target.queue[0]
			if i <= a.lt && i+message.length-1 <= dynamicSlots {
				result.Sent = append(result.Sent, SentItem{
					ID:    k,
					Tag:   message.tag,
					Start: i,
				})
				target.queue = target.queue[1:]
				a.totalSent++
				i += message.length
				k++
				continue
			}
		}
		i++
		k++
	}

	a.cycle = (currentCycle + 1) % 64

	return result
}

func (a *Arbiter) Pending(id int) []SlotTag {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id < 1 || id > a.ns+a.nm {
		return nil
	}

	target := a.frames[id]
	if target == nil {
		return nil
	}

	if target.static {
		if target.buffer == nil {
			return []SlotTag{}
		}
		return []SlotTag{target.buffer.tag}
	}

	pending := make([]SlotTag, len(target.queue))
	for index, message := range target.queue {
		pending[index] = message.tag
	}
	return pending
}

func (a *Arbiter) Stats() StatsResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	return StatsResult{
		Sent:       a.totalSent,
		Empty:      a.totalEmpty,
		Overwrites: a.totalOverwrites,
	}
}
