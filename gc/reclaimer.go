package gc

import (
	"sort"
	"strings"
	"sync"
)

const (
	maxLayerSize = int64(1_000_000_000_000)
	minCapacity  = int64(1)
	maxCapacity  = int64(1_000_000_000_000_000)
	maxProtect   = 100
)

type Layer struct {
	ID   string
	Size int64
}

type GCResult struct {
	Deleted      []string
	Freed        int64
	Insufficient bool
}

type imageState int

const (
	statePulling imageState = iota
	stateReady
)

type image struct {
	id       string
	layers   []Layer
	state    imageState
	lastUsed int64
	run      int
}

type layerInfo struct {
	size int64
	refs int
}

type Reclaimer struct {
	mu     sync.Mutex
	cap    int64
	k      int
	used   int64
	maxNow int64
	images map[string]*image
	layers map[string]*layerInfo
}

func NewReclaimer(capacity int64, k int) (*Reclaimer, error) {
	if capacity < minCapacity || capacity > maxCapacity || k < 0 || k > maxProtect {
		return nil, ErrInvalidConfig
	}
	return &Reclaimer{
		cap:    capacity,
		k:      k,
		maxNow: -1,
		images: make(map[string]*image),
		layers: make(map[string]*layerInfo),
	}, nil
}

func (r *Reclaimer) Used() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used
}

func repoOf(img string) string {
	if i := strings.LastIndex(img, ":"); i >= 0 {
		return img[:i]
	}
	return img
}

func validatePullArgs(img string, layers []Layer, now int64) error {
	if img == "" || len(layers) == 0 || now < 0 {
		return ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(layers))
	for _, l := range layers {
		if l.ID == "" || l.Size < 1 || l.Size > maxLayerSize {
			return ErrInvalidArgument
		}
		if _, dup := seen[l.ID]; dup {
			return ErrInvalidArgument
		}
		seen[l.ID] = struct{}{}
	}
	return nil
}

func validateImageArgs(img string, now int64) error {
	if img == "" || now < 0 {
		return ErrInvalidArgument
	}
	return nil
}

func (r *Reclaimer) checkClock(now int64) error {
	if now < r.maxNow {
		return ErrClockSkew
	}
	return nil
}

// beginPullLocked registers a pulling image. Caller must hold r.mu and must
// have validated arguments and clock.
func (r *Reclaimer) beginPullLocked(img string, layers []Layer, now int64) error {
	if _, ok := r.images[img]; ok {
		return ErrImageExists
	}
	var added int64
	for _, l := range layers {
		if li, ok := r.layers[l.ID]; ok {
			if li.size != l.Size {
				return &LayerConflictError{LayerID: l.ID}
			}
			continue
		}
		added += l.Size
	}
	if r.used+added > r.cap {
		return ErrNoSpace
	}
	cp := make([]Layer, len(layers))
	copy(cp, layers)
	r.images[img] = &image{id: img, layers: cp, state: statePulling}
	for _, l := range layers {
		li, ok := r.layers[l.ID]
		if !ok {
			li = &layerInfo{size: l.Size}
			r.layers[l.ID] = li
			r.used += l.Size
		}
		li.refs++
	}
	r.maxNow = now
	return nil
}

// removeImageLocked deletes an image and releases layers whose refcount
// drops to zero, returning the number of bytes freed.
func (r *Reclaimer) removeImageLocked(im *image) int64 {
	var freed int64
	for _, l := range im.layers {
		li := r.layers[l.ID]
		li.refs--
		if li.refs == 0 {
			freed += li.size
			r.used -= li.size
			delete(r.layers, l.ID)
		}
	}
	delete(r.images, im.id)
	return freed
}

func (r *Reclaimer) BeginPull(img string, layers []Layer, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validatePullArgs(img, layers, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	return r.beginPullLocked(img, layers, now)
}

func (r *Reclaimer) CommitPull(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateImageArgs(img, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	im, ok := r.images[img]
	if !ok {
		return ErrImageNotFound
	}
	if im.state != statePulling {
		return ErrImageNotPulling
	}
	im.state = stateReady
	im.lastUsed = now
	im.run = 0
	r.maxNow = now
	return nil
}

func (r *Reclaimer) AbortPull(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateImageArgs(img, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	im, ok := r.images[img]
	if !ok {
		return ErrImageNotFound
	}
	if im.state != statePulling {
		return ErrImageNotPulling
	}
	r.removeImageLocked(im)
	r.maxNow = now
	return nil
}

func (r *Reclaimer) Pull(img string, layers []Layer, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validatePullArgs(img, layers, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	if err := r.beginPullLocked(img, layers, now); err != nil {
		return err
	}
	im := r.images[img]
	im.state = stateReady
	im.lastUsed = now
	im.run = 0
	return nil
}

func (r *Reclaimer) Run(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateImageArgs(img, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	im, ok := r.images[img]
	if !ok {
		return ErrImageNotFound
	}
	if im.state == statePulling {
		return ErrImagePulling
	}
	im.run++
	im.lastUsed = now
	r.maxNow = now
	return nil
}

func (r *Reclaimer) Stop(img string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateImageArgs(img, now); err != nil {
		return err
	}
	if err := r.checkClock(now); err != nil {
		return err
	}
	im, ok := r.images[img]
	if !ok {
		return ErrImageNotFound
	}
	if im.state == statePulling {
		return ErrImagePulling
	}
	if im.run == 0 {
		return ErrNotRunning
	}
	im.run--
	im.lastUsed = now
	r.maxNow = now
	return nil
}

func (r *Reclaimer) GC(now int64, high, low int, minAge int64) (GCResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < 0 || low <= 0 || low >= high || high > 100 || minAge < 0 {
		return GCResult{}, ErrInvalidArgument
	}
	if err := r.checkClock(now); err != nil {
		return GCResult{}, err
	}
	r.maxNow = now
	if r.used*100 < int64(high)*r.cap {
		return GCResult{}, nil
	}
	need := r.used - r.cap*int64(low)/100

	protected := make(map[string]struct{})
	byRepo := make(map[string][]*image)
	for _, im := range r.images {
		if im.state != stateReady {
			continue
		}
		repo := repoOf(im.id)
		byRepo[repo] = append(byRepo[repo], im)
	}
	for _, group := range byRepo {
		sort.Slice(group, func(i, j int) bool {
			if group[i].lastUsed != group[j].lastUsed {
				return group[i].lastUsed > group[j].lastUsed
			}
			return group[i].id < group[j].id
		})
		for i := 0; i < r.k && i < len(group); i++ {
			protected[group[i].id] = struct{}{}
		}
	}

	var candidates []*image
	for _, im := range r.images {
		if im.state != stateReady || im.run != 0 {
			continue
		}
		if _, ok := protected[im.id]; ok {
			continue
		}
		if now-im.lastUsed < minAge {
			continue
		}
		candidates = append(candidates, im)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].lastUsed != candidates[j].lastUsed {
			return candidates[i].lastUsed < candidates[j].lastUsed
		}
		return candidates[i].id < candidates[j].id
	})

	res := GCResult{Deleted: []string{}}
	for _, im := range candidates {
		res.Deleted = append(res.Deleted, im.id)
		res.Freed += r.removeImageLocked(im)
		if res.Freed >= need {
			break
		}
	}
	res.Insufficient = res.Freed < need
	return res, nil
}
