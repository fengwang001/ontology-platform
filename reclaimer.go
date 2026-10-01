package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidRetention     = errors.New("retention must not be negative")
	ErrClockSkew            = errors.New("operation time is before max accepted time")
	ErrEmptyDigest          = errors.New("digest must not be empty")
	ErrInvalidSize          = errors.New("layer size must be greater than zero")
	ErrDigestExists         = errors.New("digest already exists")
	ErrObjectKindMismatch   = errors.New("object kind mismatch")
	ErrObjectSizeMismatch   = errors.New("object size mismatch")
	ErrEmptyReferenceList   = errors.New("reference list must not be empty")
	ErrDuplicateReference   = errors.New("reference list contains duplicates")
	ErrReferenceNotFound    = errors.New("referenced object does not exist")
	ErrInvalidReferenceKind = errors.New("referenced object has wrong kind")
	ErrEmptyTagName         = errors.New("tag name must not be empty")
	ErrTagTargetNotFound    = errors.New("tag target does not exist")
	ErrTagTargetIsLayer     = errors.New("tag target must not be a layer")
	ErrTagNotFound          = errors.New("tag does not exist")
)

type ObjectKind int

const (
	LayerObject ObjectKind = iota
	ManifestObject
	IndexObject
)

type Reclaimer struct {
	retention int64
	mu        sync.RWMutex
	maxNow    int64
	hasTime   bool
	objects   map[string]objectRecord
	tags      map[string]string
}

type objectRecord struct {
	kind         ObjectKind
	size         int64
	refs         []string
	alive        bool
	deadSince    int64
	hasDeadSince bool
}

func NewReclaimer(retentionMillis int64) (*Reclaimer, error) {
	if retentionMillis < 0 {
		return nil, ErrInvalidRetention
	}

	return &Reclaimer{
		retention: retentionMillis,
		objects:   make(map[string]objectRecord),
		tags:      make(map[string]string),
	}, nil
}

func (r *Reclaimer) PutLayer(now int64, digest string, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkTime(now); err != nil {
		return err
	}
	if digest == "" {
		return ErrEmptyDigest
	}
	if size <= 0 {
		return ErrInvalidSize
	}
	if existing, ok := r.objects[digest]; ok {
		if existing.kind != LayerObject {
			return ErrObjectKindMismatch
		}
		if existing.size != size {
			return ErrObjectSizeMismatch
		}
		r.acceptTime(now)
		return nil
	}

	r.objects[digest] = objectRecord{kind: LayerObject, size: size}
	r.recompute(now)
	r.acceptTime(now)
	return nil
}

func (r *Reclaimer) PutManifest(now int64, digest string, layerDigests []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.putReferencingObject(now, digest, layerDigests, ManifestObject, LayerObject); err != nil {
		return err
	}
	return nil
}

func (r *Reclaimer) PutIndex(now int64, digest string, manifestDigests []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.putReferencingObject(now, digest, manifestDigests, IndexObject, ManifestObject); err != nil {
		return err
	}
	return nil
}

func (r *Reclaimer) Tag(now int64, name, targetDigest string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkTime(now); err != nil {
		return err
	}
	if name == "" {
		return ErrEmptyTagName
	}
	target, ok := r.objects[targetDigest]
	if !ok {
		return ErrTagTargetNotFound
	}
	if target.kind == LayerObject {
		return ErrTagTargetIsLayer
	}

	r.tags[name] = targetDigest
	r.recompute(now)
	r.acceptTime(now)
	return nil
}

func (r *Reclaimer) Untag(now int64, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkTime(now); err != nil {
		return err
	}
	if name == "" {
		return ErrEmptyTagName
	}
	if _, ok := r.tags[name]; !ok {
		return ErrTagNotFound
	}

	delete(r.tags, name)
	r.recompute(now)
	r.acceptTime(now)
	return nil
}

func (r *Reclaimer) GC(now int64) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.checkTime(now); err != nil {
		return nil, err
	}
	r.acceptTime(now)

	referrers := make(map[string]map[string]struct{})
	for digest, obj := range r.objects {
		if obj.kind == LayerObject {
			continue
		}
		for _, referenced := range obj.refs {
			if referrers[referenced] == nil {
				referrers[referenced] = make(map[string]struct{})
			}
			referrers[referenced][digest] = struct{}{}
		}
	}

	deleted := make([]string, 0)
	for _, kind := range []ObjectKind{IndexObject, ManifestObject, LayerObject} {
		candidates := make([]string, 0)
		for digest, obj := range r.objects {
			if obj.kind == kind && !obj.alive && obj.hasDeadSince && now-obj.deadSince >= r.retention {
				candidates = append(candidates, digest)
			}
		}
		sort.Strings(candidates)

		for _, digest := range candidates {
			obj, ok := r.objects[digest]
			if !ok || obj.alive || !obj.hasDeadSince || now-obj.deadSince < r.retention {
				continue
			}
			if len(referrers[digest]) != 0 {
				continue
			}

			for _, referenced := range obj.refs {
				delete(referrers[referenced], digest)
			}
			delete(referrers, digest)
			delete(r.objects, digest)
			deleted = append(deleted, digest)
		}
	}

	return deleted, nil
}

func (r *Reclaimer) Exists(digest string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.objects[digest]
	return ok
}

func (r *Reclaimer) Live(digest string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.objects[digest].alive
}

func (r *Reclaimer) checkTime(now int64) error {
	if r.hasTime && now < r.maxNow {
		return ErrClockSkew
	}
	return nil
}

func (r *Reclaimer) acceptTime(now int64) {
	r.hasTime = true
	r.maxNow = now
}

func (r *Reclaimer) putReferencingObject(now int64, digest string, refs []string, kind, requiredKind ObjectKind) error {
	if err := r.checkTime(now); err != nil {
		return err
	}
	if digest == "" {
		return ErrEmptyDigest
	}
	if _, ok := r.objects[digest]; ok {
		return ErrDigestExists
	}
	if len(refs) == 0 {
		return ErrEmptyReferenceList
	}

	seen := make(map[string]struct{}, len(refs))
	for _, referenced := range refs {
		if _, ok := seen[referenced]; ok {
			return ErrDuplicateReference
		}
		seen[referenced] = struct{}{}
	}

	for _, referenced := range refs {
		if _, ok := r.objects[referenced]; !ok {
			return ErrReferenceNotFound
		}
	}
	for _, referenced := range refs {
		if r.objects[referenced].kind != requiredKind {
			return ErrInvalidReferenceKind
		}
	}

	copiedRefs := append([]string(nil), refs...)
	r.objects[digest] = objectRecord{kind: kind, refs: copiedRefs}
	r.recompute(now)
	r.acceptTime(now)
	return nil
}

func (r *Reclaimer) recompute(now int64) {
	live := make(map[string]bool)

	for _, target := range r.tags {
		if obj, ok := r.objects[target]; ok && obj.kind != LayerObject {
			live[target] = true
		}
	}

	queue := make([]string, 0, len(live))
	for digest := range live {
		queue = append(queue, digest)
	}
	for len(queue) > 0 {
		digest := queue[0]
		queue = queue[1:]

		obj := r.objects[digest]
		for _, referenced := range obj.refs {
			if !live[referenced] {
				live[referenced] = true
				queue = append(queue, referenced)
			}
		}
	}

	for digest, obj := range r.objects {
		switch {
		case live[digest] && !obj.alive:
			obj.alive = true
			obj.hasDeadSince = false
			obj.deadSince = 0
		case !live[digest] && (obj.alive || !obj.hasDeadSince):
			obj.alive = false
			obj.hasDeadSince = true
			obj.deadSince = now
		}
		r.objects[digest] = obj
	}
}
