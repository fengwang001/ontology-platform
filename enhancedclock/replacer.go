package enhancedclock

import (
	"errors"
	"sync"
)

var (
	ErrInvalidFrameCount = errors.New("frame count must be at least 1")
	ErrNegativePage      = errors.New("page number must not be negative")
	ErrPageNotResident   = errors.New("page is not resident")
	ErrPageNotDirty      = errors.New("page is not dirty")
)

type Frame struct {
	Page       int
	Referenced bool
	Dirty      bool
	Occupied   bool
}

type AccessResult struct {
	PageFault     bool
	Evicted       bool
	EvictedPage   int
	WriteBack     bool
	Frame         int
	OldPointer    int
	NewPointer    int
	VictimPass    string
	ScannedFrames []int
	ClearedFrames []int
}

type Replacer struct {
	mu         sync.Mutex
	frames     []Frame
	pointer    int
	writeBacks int
}

func New(frameCount int) (*Replacer, error) {
	if frameCount < 1 {
		return nil, ErrInvalidFrameCount
	}

	frames := make([]Frame, frameCount)
	for index := range frames {
		frames[index].Page = -1
	}

	return &Replacer{frames: frames}, nil
}

func (r *Replacer) Access(page int, write bool) (AccessResult, error) {
	if page < 0 {
		return AccessResult{}, ErrNegativePage
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	result := AccessResult{OldPointer: r.pointer, NewPointer: r.pointer}

	for index := range r.frames {
		frame := &r.frames[index]
		if frame.Occupied && frame.Page == page {
			frame.Referenced = true
			if write {
				frame.Dirty = true
			}
			result.Frame = index
			return result, nil
		}
	}

	result.PageFault = true

	for index := range r.frames {
		if !r.frames[index].Occupied {
			r.frames[index] = Frame{
				Page:       page,
				Referenced: true,
				Dirty:      write,
				Occupied:   true,
			}
			result.Frame = index
			return result, nil
		}
	}

	victim, victimPass, scanned, cleared := r.selectVictim()
	evictedFrame := &r.frames[victim]
	result.Evicted = true
	result.EvictedPage = evictedFrame.Page
	result.WriteBack = evictedFrame.Dirty
	result.Frame = victim
	result.VictimPass = victimPass
	result.ScannedFrames = scanned
	result.ClearedFrames = cleared
	if result.WriteBack {
		r.writeBacks++
	}

	*evictedFrame = Frame{
		Page:       page,
		Referenced: true,
		Dirty:      write,
		Occupied:   true,
	}
	r.pointer = (victim + 1) % len(r.frames)
	result.NewPointer = r.pointer

	return result, nil
}

func (r *Replacer) selectVictim() (int, string, []int, []int) {
	scanned := make([]int, 0, len(r.frames)*2)
	cleared := make([]int, 0)

	for {
		for offset := 0; offset < len(r.frames); offset++ {
			index := (r.pointer + offset) % len(r.frames)
			scanned = append(scanned, index)
			frame := &r.frames[index]
			if !frame.Referenced && !frame.Dirty {
				return index, "A", scanned, cleared
			}
		}

		for offset := 0; offset < len(r.frames); offset++ {
			index := (r.pointer + offset) % len(r.frames)
			scanned = append(scanned, index)
			frame := &r.frames[index]
			if frame.Referenced {
				frame.Referenced = false
				cleared = append(cleared, index)
				continue
			}
			if frame.Dirty {
				return index, "B", scanned, cleared
			}
		}
	}
}

func (r *Replacer) Flush(page int) error {
	if page < 0 {
		return ErrNegativePage
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for index := range r.frames {
		frame := &r.frames[index]
		if frame.Occupied && frame.Page == page {
			if !frame.Dirty {
				return ErrPageNotDirty
			}
			r.writeBacks++
			frame.Dirty = false
			return nil
		}
	}

	return ErrPageNotResident
}

func (r *Replacer) Pointer() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pointer
}

func (r *Replacer) WriteBacks() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeBacks
}

func (r *Replacer) Frames() []Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Frame(nil), r.frames...)
}
