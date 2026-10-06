package layerconfig

// Value is a typed configuration value. Exactly one field is meaningful,
// according to the key schema.
type Value struct {
	Str  string
	Int  int
	Bool bool
	List []string
}

// Op identifies the kind of change in a publication.
type Op int

const (
	OpWrite Op = iota + 1
	OpCancel
	OpClear
	OpLock
	OpUnlock
)

// Change is a single mutation of one (scope,key) in a publication.
type Change struct {
	Op    Op
	Scope Scope
	Key   string
	Value Value
}

// state is an immutable snapshot stored per version. The maps are persistent
// and structurally shared with adjacent versions.
type state struct {
	version int

	// writes maps (scope,key) -> writeEntry. Absence means no write at that
	// scope for that key (or it was cleared).
	writes *pmap

	// locks maps (scope,key) -> present means the key is locked there.
	locks *pmap

	// exists maps scope identity (env/region/instance) -> present, tracking
	// every identity that has ever appeared in a write.
	exists *pmap
}

func initialState() *state {
	return &state{
		version: 0,
		writes:  emptyPmap(),
		locks:   emptyPmap(),
		exists:  emptyPmap(),
	}
}

// writeKind distinguishes a concrete value from an explicit cancellation.
type writeKind int

const (
	writeValue  writeKind = 1
	writeCancel writeKind = 2
)

// writeEntry is the immutable record stored per (scope,key).
type writeEntry struct {
	kind  writeKind
	value Value
}

// entry is the uniform payload of pmap.
type entry struct {
	write *writeEntry
	lock  bool
	unit  bool
}

// normalizeValue copies list contents so later caller mutation cannot leak
// into immutable state.
func normalizeValue(s Schema, v Value) Value {
	if s.Type == TypeStringList && v.List != nil {
		cp := make([]string, len(v.List))
		copy(cp, v.List)
		v.List = cp
	}
	return v
}

// workingState is the mutable scratch set used while folding one batch. It is
// never shared; only the committed immutable state escapes applyBatch.
type workingState struct {
	writes map[string]entry
	locks  map[string]entry
	exists map[string]entry
}

func newWorkingState(base *state) *workingState {
	w := &workingState{
		writes: map[string]entry{},
		locks:  map[string]entry{},
		exists: map[string]entry{},
	}
	base.writes.forEach(func(k string, e entry) bool { w.writes[k] = e; return true })
	base.locks.forEach(func(k string, e entry) bool { w.locks[k] = e; return true })
	base.exists.forEach(func(k string, e entry) bool { w.exists[k] = e; return true })
	return w
}

// rebuildPmap returns a persistent map reflecting next, reusing the base
// persistent map and applying only differences so unchanged shards are
// shared.
func rebuildPmap(base *pmap, next map[string]entry) *pmap {
	out := base
	base.forEach(func(k string, e entry) bool {
		if _, ok := next[k]; !ok {
			out = out.delete(k)
		}
		return true
	})
	for k, v := range next {
		if old, ok := base.get(k); ok && entriesEqual(old, v) {
			continue
		}
		out = out.set(k, v)
	}
	return out
}

func entriesEqual(a, b entry) bool {
	if a.lock != b.lock || a.unit != b.unit {
		return false
	}
	if (a.write == nil) != (b.write == nil) {
		return false
	}
	if a.write == nil {
		return true
	}
	return writesEqual(*a.write, *b.write)
}

func writesEqual(a, b writeEntry) bool {
	if a.kind != b.kind {
		return false
	}
	av, bv := a.value, b.value
	if av.Str != bv.Str || av.Int != bv.Int || av.Bool != bv.Bool {
		return false
	}
	if len(av.List) != len(bv.List) {
		return false
	}
	for i := range av.List {
		if av.List[i] != bv.List[i] {
			return false
		}
	}
	return true
}

// commit converts the mutable working set into an immutable state using
// structural sharing.
func (w *workingState) commit(base *state, newVersion int) *state {
	return &state{
		version: newVersion,
		writes:  rebuildPmap(base.writes, w.writes),
		locks:   rebuildPmap(base.locks, w.locks),
		exists:  rebuildPmap(base.exists, w.exists),
	}
}

// identityKeys returns existence identities implied by a write scope.
func identityKeys(sc Scope, layer Layer) []string {
	var keys []string
	if layer >= LayerEnv {
		keys = append(keys, existenceKey(sc.Env, "", ""))
	}
	if layer >= LayerRegion {
		keys = append(keys, existenceKey(sc.Env, sc.Region, ""))
	}
	if layer >= LayerInstance {
		keys = append(keys, existenceKey(sc.Env, sc.Region, sc.Instance))
	}
	return keys
}
