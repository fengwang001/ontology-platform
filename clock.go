package clock

import (
	"errors"
	"sync"
)

var (
	ErrInvalidFrameCount = errors.New("frame count must be greater than zero")
	ErrNegativePage      = errors.New("page number must not be negative")
	ErrPageNotResident   = errors.New("page is not resident")
	ErrPageNotDirty      = errors.New("page is not dirty")
)

type Frame struct {
	Page       int
	Occupied   bool
	Referenced bool
	Dirty      bool
}

type Eviction struct {
	Frame        int
	Page         int
	Dirty        bool
	WriteBacks   int
	SelectedPass string
}

type AccessResult struct {
	Page       int
	Write      bool
	Hit        bool
	Frame      int
	Eviction   *Eviction
	WriteBacks int
}

type FlushResult struct {
	Page       int
	Frame      int
	WriteBacks int
}

type Snapshot struct {
	Frames     []Frame
	Pointer    int
	WriteBacks int
}

type ClockReplacer struct {
	mu         sync.Mutex
	frames     []Frame
	pointer    int
	writeBacks int
}

func New(n int) (*ClockReplacer, error) {
	if n < 1 {
		return nil, ErrInvalidFrameCount
	}

	return &ClockReplacer{
		frames: make([]Frame, n),
	}, nil
}

func (r *ClockReplacer) Access(page int, write bool) (AccessResult, error) {
	if page < 0 {
		return AccessResult{}, ErrNegativePage
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	result := AccessResult{
		Page:       page,
		Write:      write,
		WriteBacks: r.writeBacks,
	}

	if frame, ok := r.pageFrame(page); ok {
		result.Hit = true
		result.Frame = frame
		r.frames[frame].Referenced = true
		if write {
			r.frames[frame].Dirty = true
		}
		result.WriteBacks = r.writeBacks
		return result, nil
	}

	if frame, ok := r.freeFrame(); ok {
		r.frames[frame] = Frame{
			Page:       page,
			Occupied:   true,
			Referenced: true,
			Dirty:      write,
		}
		result.Frame = frame
		result.WriteBacks = r.writeBacks
		return result, nil
	}

	eviction := r.evict()
	r.frames[eviction.Frame] = Frame{
		Page:       page,
		Occupied:   true,
		Referenced: true,
		Dirty:      write,
	}
	r.pointer = (eviction.Frame + 1) % len(r.frames)

	result.Frame = eviction.Frame
	result.Eviction = &eviction
	result.WriteBacks = r.writeBacks
	return result, nil
}

func (r *ClockReplacer) Flush(page int) (FlushResult, error) {
	if page < 0 {
		return FlushResult{}, ErrNegativePage
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	frame, ok := r.pageFrame(page)
	if !ok {
		return FlushResult{}, ErrPageNotResident
	}
	if !r.frames[frame].Dirty {
		return FlushResult{}, ErrPageNotDirty
	}

	r.frames[frame].Dirty = false
	r.writeBacks++

	return FlushResult{
		Page:       page,
		Frame:      frame,
		WriteBacks: r.writeBacks,
	}, nil
}

func (r *ClockReplacer) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	frames := append([]Frame(nil), r.frames...)
	return Snapshot{
		Frames:     frames,
		Pointer:    r.pointer,
		WriteBacks: r.writeBacks,
	}
}

func (r *ClockReplacer) pageFrame(page int) (int, bool) {
	for frame, resident := range r.frames {
		if resident.Occupied && resident.Page == page {
			return frame, true
		}
	}
	return 0, false
}

func (r *ClockReplacer) freeFrame() (int, bool) {
	for frame, resident := range r.frames {
		if !resident.Occupied {
			return frame, true
		}
	}
	return 0, false
}

func (r *ClockReplacer) evict() Eviction {
	for {
		if frame, ok := r.scanPassA(); ok {
			return r.finishEviction(frame, "A")
		}

		if frame, ok := r.scanPassB(); ok {
			return r.finishEviction(frame, "B")
		}
	}
}

func (r *ClockReplacer) scanPassA() (int, bool) {
	for offset := 0; offset < len(r.frames); offset++ {
		frame := (r.pointer + offset) % len(r.frames)
		if !r.frames[frame].Referenced && !r.frames[frame].Dirty {
			return frame, true
		}
	}
	return 0, false
}

func (r *ClockReplacer) scanPassB() (int, bool) {
	for offset := 0; offset < len(r.frames); offset++ {
		frame := (r.pointer + offset) % len(r.frames)
		if !r.frames[frame].Referenced && r.frames[frame].Dirty {
			return frame, true
		}
		if r.frames[frame].Referenced {
			r.frames[frame].Referenced = false
		}
	}
	return 0, false
}

func (r *ClockReplacer) finishEviction(frame int, selectedPass string) Eviction {
	victim := r.frames[frame]
	if victim.Dirty {
		r.writeBacks++
	}

	return Eviction{
		Frame:        frame,
		Page:         victim.Page,
		Dirty:        victim.Dirty,
		WriteBacks:   r.writeBacks,
		SelectedPass: selectedPass,
	}
}
