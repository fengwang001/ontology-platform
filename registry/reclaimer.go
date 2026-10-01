package registry

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidRetention        = errors.New("retention must not be negative")
	ErrClockMovedBackward      = errors.New("clock moved backward")
	ErrEmptyDigest             = errors.New("digest must not be empty")
	ErrInvalidSize             = errors.New("layer size must be positive")
	ErrDigestAlreadyExists     = errors.New("digest already exists")
	ErrDigestMismatch          = errors.New("digest already exists with a different kind or size")
	ErrEmptyReferenceList      = errors.New("reference list must not be empty")
	ErrDuplicateReference      = errors.New("reference list contains a duplicate")
	ErrReferencedDigestMissing = errors.New("referenced digest does not exist")
	ErrInvalidReferenceKind    = errors.New("referenced digest has an invalid kind")
	ErrEmptyTagName            = errors.New("tag name must not be empty")
	ErrTagTargetMissing        = errors.New("tag target does not exist")
	ErrTagTargetIsLayer        = errors.New("tag target must not be a layer")
	ErrTagNotFound             = errors.New("tag does not exist")
)

type kind int

const (
	layerKind kind = iota
	manifestKind
	indexKind
)

type object struct {
	kind         kind
	size         int64
	layers       []string
	manifests    []string
	deadSince    int64
	deadSinceSet bool
	live         bool
}

type Reclaimer struct {
	mu        sync.Mutex
	retention int64
	maxNow    int64
	maxNowSet bool
	objects   map[string]*object
	tags      map[string]string
}

func NewReclaimer(retentionMs int64) (*Reclaimer, error) {
	if retentionMs < 0 {
		return nil, ErrInvalidRetention
	}
	return &Reclaimer{
		retention: retentionMs,
		objects:   make(map[string]*object),
		tags:      make(map[string]string),
	}, nil
}

func (r *Reclaimer) PutLayer(digest string, size int64, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if digest == "" {
		return ErrEmptyDigest
	}
	if size <= 0 {
		return ErrInvalidSize
	}
	if existing, ok := r.objects[digest]; ok {
		if existing.kind != layerKind || existing.size != size {
			return ErrDigestMismatch
		}
		r.acceptClock(now)
		return nil
	}

	r.objects[digest] = &object{
		kind:         layerKind,
		size:         size,
		deadSince:    now,
		deadSinceSet: true,
	}
	r.recomputeLiveness(now)
	r.acceptClock(now)
	return nil
}

func (r *Reclaimer) PutManifest(digest string, layerDigests []string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if digest == "" {
		return ErrEmptyDigest
	}
	if _, ok := r.objects[digest]; ok {
		return ErrDigestAlreadyExists
	}
	refs, err := r.validateReferences(layerDigests, layerKind)
	if err != nil {
		return err
	}

	r.objects[digest] = &object{
		kind:         manifestKind,
		layers:       refs,
		deadSince:    now,
		deadSinceSet: true,
	}
	r.recomputeLiveness(now)
	r.acceptClock(now)
	return nil
}

func (r *Reclaimer) PutIndex(digest string, manifestDigests []string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if digest == "" {
		return ErrEmptyDigest
	}
	if _, ok := r.objects[digest]; ok {
		return ErrDigestAlreadyExists
	}
	refs, err := r.validateReferences(manifestDigests, manifestKind)
	if err != nil {
		return err
	}

	r.objects[digest] = &object{
		kind:         indexKind,
		manifests:    refs,
		deadSince:    now,
		deadSinceSet: true,
	}
	r.recomputeLiveness(now)
	r.acceptClock(now)
	return nil
}

func (r *Reclaimer) Tag(name, digest string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if name == "" {
		return ErrEmptyTagName
	}
	target, ok := r.objects[digest]
	if !ok {
		return ErrTagTargetMissing
	}
	if target.kind == layerKind {
		return ErrTagTargetIsLayer
	}

	r.tags[name] = digest
	r.recomputeLiveness(now)
	r.acceptClock(now)
	return nil
}

func (r *Reclaimer) Untag(name string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return err
	}
	if name == "" {
		return ErrEmptyTagName
	}
	if _, ok := r.tags[name]; !ok {
		return ErrTagNotFound
	}

	delete(r.tags, name)
	r.recomputeLiveness(now)
	r.acceptClock(now)
	return nil
}

func (r *Reclaimer) GC(now int64) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkClock(now); err != nil {
		return nil, err
	}

	deleted := make([]string, 0)
	for _, targetKind := range []kind{indexKind, manifestKind, layerKind} {
		candidates := make([]string, 0)
		for digest, obj := range r.objects {
			if obj.kind != targetKind || obj.live || !obj.deadSinceSet || now-obj.deadSince < r.retention {
				continue
			}
			if r.hasReferrer(digest) {
				continue
			}
			candidates = append(candidates, digest)
		}
		sort.Strings(candidates)
		for _, digest := range candidates {
			obj, ok := r.objects[digest]
			if !ok || obj.kind != targetKind || obj.live || !obj.deadSinceSet || now-obj.deadSince < r.retention || r.hasReferrer(digest) {
				continue
			}
			delete(r.objects, digest)
			deleted = append(deleted, digest)
		}
	}

	r.acceptClock(now)
	return deleted, nil
}

func (r *Reclaimer) Exists(digest string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.objects[digest]
	return ok
}

func (r *Reclaimer) Live(digest string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	obj, ok := r.objects[digest]
	return ok && obj.live
}

func (r *Reclaimer) checkClock(now int64) error {
	if r.maxNowSet && now < r.maxNow {
		return ErrClockMovedBackward
	}
	return nil
}

func (r *Reclaimer) acceptClock(now int64) {
	r.maxNow = now
	r.maxNowSet = true
}

func (r *Reclaimer) validateReferences(references []string, expected kind) ([]string, error) {
	if len(references) == 0 {
		return nil, ErrEmptyReferenceList
	}

	seen := make(map[string]struct{}, len(references))
	refs := make([]string, len(references))
	copy(refs, references)
	for _, digest := range refs {
		if _, ok := seen[digest]; ok {
			return nil, ErrDuplicateReference
		}
		seen[digest] = struct{}{}

		obj, ok := r.objects[digest]
		if !ok {
			return nil, ErrReferencedDigestMissing
		}
		if obj.kind != expected {
			return nil, ErrInvalidReferenceKind
		}
	}
	return refs, nil
}

func (r *Reclaimer) recomputeLiveness(now int64) {
	live := make(map[string]bool)
	queue := make([]string, 0)

	for _, digest := range r.tags {
		if obj, ok := r.objects[digest]; ok && obj.kind != layerKind && !live[digest] {
			live[digest] = true
			queue = append(queue, digest)
		}
	}

	for len(queue) > 0 {
		digest := queue[0]
		queue = queue[1:]
		obj := r.objects[digest]
		switch obj.kind {
		case indexKind:
			for _, manifestDigest := range obj.manifests {
				if !live[manifestDigest] {
					live[manifestDigest] = true
					queue = append(queue, manifestDigest)
				}
			}
		case manifestKind:
			for _, layerDigest := range obj.layers {
				live[layerDigest] = true
			}
		}
	}

	for digest, obj := range r.objects {
		wasLive := obj.live
		obj.live = live[digest]
		switch {
		case obj.live:
			obj.deadSince = 0
			obj.deadSinceSet = false
		case wasLive || !obj.deadSinceSet:
			obj.deadSince = now
			obj.deadSinceSet = true
		}
	}
}

func (r *Reclaimer) hasReferrer(digest string) bool {
	for _, obj := range r.objects {
		switch obj.kind {
		case manifestKind:
			for _, layerDigest := range obj.layers {
				if layerDigest == digest {
					return true
				}
			}
		case indexKind:
			for _, manifestDigest := range obj.manifests {
				if manifestDigest == digest {
					return true
				}
			}
		}
	}
	return false
}
