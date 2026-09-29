package materializedview

import "sync"

type RebuildProgress struct {
	// Active reports whether the rebuild can currently be stepped or committed.
	Active bool
	// Recoverable reports whether a crash left a checkpoint that can be resumed.
	Recoverable bool
	BlockSize   int
	Processed   int
	TotalEvents int
	Complete    bool
	Shadow      CountView
}

type ViewSnapshot struct {
	Generation int64
	View       CountView
}

type rebuildState struct {
	active    bool
	blockSize int
	processed int
	shadow    CountView
}

type Rebuilder struct {
	opMu       sync.Mutex
	mu         sync.RWMutex
	events     []Event
	view       CountView
	generation int64
	rebuild    *rebuildState
}

// NewRebuilder creates a rebuild manager over a snapshot of the ordered source.
func NewRebuilder(events []Event, initialView CountView, initialGeneration int64) *Rebuilder {
	eventsSnapshot := make([]Event, len(events))
	copy(eventsSnapshot, events)

	return &Rebuilder{
		events:     eventsSnapshot,
		view:       cloneView(initialView),
		generation: initialGeneration,
	}
}

// BeginRebuild starts a fresh rebuild or resumes a checkpoint left by a crash.
// The block size argument is ignored while resuming an existing checkpoint.
func (r *Rebuilder) BeginRebuild(blockSize int) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if blockSize <= 0 {
		return ErrInvalidBlockSize
	}

	if r.rebuild != nil && r.rebuild.active {
		return ErrRebuildAlreadyOpen
	}

	if r.rebuild != nil {
		r.rebuild.active = true
		return nil
	}

	r.rebuild = &rebuildState{
		active:    true,
		blockSize: blockSize,
		processed: 0,
		shadow:    CountView{},
	}
	return nil
}

// StepRebuild processes one chunk and publishes its result only as a checkpoint.
func (r *Rebuilder) StepRebuild() (RebuildProgress, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.RLock()
	if r.rebuild == nil || !r.rebuild.active {
		r.mu.RUnlock()
		return RebuildProgress{}, ErrNoRebuild
	}

	start := r.rebuild.processed
	blockSize := r.rebuild.blockSize
	shadow := r.rebuild.shadow
	r.mu.RUnlock()

	if start == len(r.events) {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.progressLocked(), nil
	}

	end := start + blockSize
	if end > len(r.events) || end < start {
		end = len(r.events)
	}

	for _, event := range r.events[start:end] {
		shadow[event.Key]++
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rebuild == nil || !r.rebuild.active {
		return RebuildProgress{}, ErrNoRebuild
	}
	if r.rebuild.processed != start {
		return RebuildProgress{}, ErrNoRebuild
	}

	r.rebuild.processed = end
	r.rebuild.shadow = shadow

	return r.progressLocked(), nil
}

// CommitRebuild atomically publishes a complete shadow view and increments generation.
func (r *Rebuilder) CommitRebuild() error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rebuild == nil || !r.rebuild.active {
		return ErrNoRebuild
	}
	if r.rebuild.processed != len(r.events) {
		return ErrRebuildIncomplete
	}

	r.view = cloneView(r.rebuild.shadow)
	r.generation++
	r.rebuild = nil
	return nil
}

// CrashRebuild simulates a crash after the most recent completed chunk checkpoint.
func (r *Rebuilder) CrashRebuild() error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rebuild == nil || !r.rebuild.active {
		return ErrNoRebuild
	}

	r.rebuild.active = false
	return nil
}

// Progress returns a copy of rebuild metadata and shadow checkpoint contents.
func (r *Rebuilder) Progress() RebuildProgress {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.progressLocked()
}

// View returns a copy of the currently committed view.
func (r *Rebuilder) View() CountView {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneView(r.view)
}

// Generation returns the current committed view generation.
func (r *Rebuilder) Generation() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

// Snapshot returns an atomic generation/view pair from one committed boundary.
func (r *Rebuilder) Snapshot() ViewSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return ViewSnapshot{
		Generation: r.generation,
		View:       cloneView(r.view),
	}
}

func (r *Rebuilder) progressLocked() RebuildProgress {
	progress := RebuildProgress{
		TotalEvents: len(r.events),
	}

	if r.rebuild == nil {
		progress.Shadow = CountView{}
		return progress
	}

	progress.Active = r.rebuild.active
	progress.Recoverable = !r.rebuild.active
	progress.BlockSize = r.rebuild.blockSize
	progress.Processed = r.rebuild.processed
	progress.Complete = r.rebuild.processed == len(r.events)
	progress.Shadow = cloneView(r.rebuild.shadow)
	return progress
}
