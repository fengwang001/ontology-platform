// Package keygroup implements key-group based state partitioning and
// redistribution across instances (parallelism scaling).
//
// Keys are mapped to a fixed number of key groups, and key groups are assigned
// to instances as disjoint contiguous ranges that cover every group exactly
// once. When parallelism changes, only key groups whose owning instance differs
// are migrated as a whole.
package keygroup

import (
	"bytes"
	"errors"
	"hash/fnv"
	"log/slog"
	"sort"
	"sync"
)

// Sentinel errors returned for distinguishable rejection reasons.
var (
	ErrInvalidGroupCount  = errors.New("invalid group count: must be greater than zero")
	ErrInvalidParallelism = errors.New("invalid parallelism: must be within [1, group count]")
	ErrEmptyKey           = errors.New("invalid key: must not be empty")
	ErrInvalidLogger      = errors.New("invalid logger: must not be nil")
)

// Range is the contiguous, inclusive key-group interval owned by an instance.
type Range struct {
	Instance int
	Start    int
	End      int
}

// Migration describes one key group that moves between instances.
type Migration struct {
	Group       int
	From        int
	To          int
	MovedKeys   []string
	MovedKeyNum int
}

// Move records the ownership change of a single key.
type Move struct {
	Key  string
	From int
	To   int
}

// Result is the fully consistent, point-in-time outcome of a rescale.
type Result struct {
	OldParallelism int
	NewParallelism int
	Ranges         []Range
	Ownership      []int
	Migrations     []Migration
	Moves          []Move
	MovedKeyNum    int
	// States maps every key to a copy of its value. It is included so that
	// callers can verify that no key is lost or duplicated after rescaling.
	States map[string][]byte
}

// Router keeps keyed state, maps keys to key groups, and assigns groups to
// instances as contiguous ranges. The zero value is not usable; use New.
type Router struct {
	mu          sync.RWMutex
	logger      *slog.Logger
	groupCount  int
	parallelism int
	values      map[string][]byte
	groups      map[string]int
}

// Option configures a Router.
type Option func(*Router) error

// WithLogger sets the structured logger used to record inputs, ownership and
// migration decisions. Defaults to slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(r *Router) error {
		if logger == nil {
			return ErrInvalidLogger
		}
		r.logger = logger
		return nil
	}
}

// New creates a Router with groupCount fixed key groups and initialParallelism
// instances.
func New(groupCount, initialParallelism int, opts ...Option) (*Router, error) {
	if groupCount <= 0 {
		return nil, ErrInvalidGroupCount
	}
	if initialParallelism < 1 || initialParallelism > groupCount {
		return nil, ErrInvalidParallelism
	}
	r := &Router{
		logger:      slog.Default(),
		groupCount:  groupCount,
		parallelism: initialParallelism,
		values:      make(map[string][]byte),
		groups:      make(map[string]int),
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	r.logger.Info("keygroup router created",
		slog.Int("group_count", groupCount),
		slog.Int("parallelism", initialParallelism),
		slog.Any("ranges", ranges(groupCount, initialParallelism)))
	return r, nil
}

// Put sets the value of a key.
func (r *Router) Put(key string, value []byte) error {
	if key == "" {
		r.logger.Warn("put rejected", slog.String("reason", ErrEmptyKey.Error()))
		return ErrEmptyKey
	}
	group := GroupOf(key, r.groupCount)

	r.mu.Lock()
	instance := ownerOf(group, r.groupCount, r.parallelism)
	r.values[key] = bytes.Clone(value)
	r.groups[key] = group
	parallelism := r.parallelism
	r.mu.Unlock()

	r.logger.Info("key stored",
		slog.String("key", key),
		slog.Int("group", group),
		slog.Int("instance", instance),
		slog.Int("parallelism", parallelism),
		slog.Int("value_bytes", len(value)))
	return nil
}

// Rescale changes the number of instances and reports group migrations.
func (r *Router) Rescale(newParallelism int) (*Result, error) {
	if newParallelism < 1 || newParallelism > r.groupCount {
		r.logger.Warn("rescale rejected",
			slog.Int("requested_parallelism", newParallelism),
			slog.Int("group_count", r.groupCount),
			slog.String("reason", ErrInvalidParallelism.Error()))
		return nil, ErrInvalidParallelism
	}

	r.mu.Lock()
	oldParallelism := r.parallelism
	if newParallelism == oldParallelism {
		result := r.snapshotLocked(oldParallelism)
		r.mu.Unlock()

		r.logger.Info("rescale is a no-op",
			slog.Int("old_parallelism", oldParallelism),
			slog.Int("new_parallelism", newParallelism),
			slog.Int("moved_keys", 0))
		return result, nil
	}

	oldOwnership := ownership(r.groupCount, oldParallelism)
	newOwnership := ownership(r.groupCount, newParallelism)

	migrationsByGroup := make(map[int]*Migration)
	movesByGroup := make(map[int][]Move)
	movedKeyNum := 0

	// Iterate keys in sorted order so repeated identical input sequences
	// produce byte-for-byte identical output ordering.
	keys := make([]string, 0, len(r.values))
	for key := range r.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		group := r.groups[key]
		from := oldOwnership[group]
		to := newOwnership[group]
		if from == to {
			continue
		}
		migration := migrationsByGroup[group]
		if migration == nil {
			migration = &Migration{Group: group, From: from, To: to}
			migrationsByGroup[group] = migration
		}
		migration.MovedKeys = append(migration.MovedKeys, key)
		migration.MovedKeyNum++
		movesByGroup[group] = append(movesByGroup[group], Move{Key: key, From: from, To: to})
		movedKeyNum++
	}

	migratedGroups := make([]int, 0, len(migrationsByGroup))
	for group := range migrationsByGroup {
		migratedGroups = append(migratedGroups, group)
	}
	sort.Ints(migratedGroups)

	migrations := make([]Migration, 0, len(migratedGroups))
	moves := make([]Move, 0, movedKeyNum)
	for _, group := range migratedGroups {
		migrations = append(migrations, *migrationsByGroup[group])
		moves = append(moves, movesByGroup[group]...)
	}

	r.parallelism = newParallelism
	result := &Result{
		OldParallelism: oldParallelism,
		NewParallelism: newParallelism,
		Ranges:         ranges(r.groupCount, newParallelism),
		Ownership:      newOwnership,
		Migrations:     migrations,
		Moves:          moves,
		MovedKeyNum:    movedKeyNum,
		States:         cloneValues(r.values, keys),
	}
	r.mu.Unlock()

	r.logger.Info("rescale completed",
		slog.Int("old_parallelism", oldParallelism),
		slog.Int("new_parallelism", newParallelism),
		slog.Any("old_ranges", ranges(r.groupCount, oldParallelism)),
		slog.Any("new_ranges", result.Ranges),
		slog.Any("new_ownership", result.Ownership),
		slog.Int("migrated_groups", len(migrations)),
		slog.Int("moved_keys", movedKeyNum))
	for _, migration := range migrations {
		r.logger.Info("key group migrated",
			slog.Int("group", migration.Group),
			slog.Int("from", migration.From),
			slog.Int("to", migration.To),
			slog.Int("moved_key_count", migration.MovedKeyNum),
			slog.Any("moved_keys", migration.MovedKeys),
			slog.String("reason", "owning instance differs under contiguous range reassignment"))
	}
	return result, nil
}

// GroupCount reports the fixed number of key groups.
func (r *Router) GroupCount() int { return r.groupCount }

// Parallelism reports the current number of instances.
func (r *Router) Parallelism() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parallelism
}

// Snapshot returns a consistent deep copy of the current state.
func (r *Router) Snapshot() *Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked(r.parallelism)
}

// GroupOf maps a non-empty key to a key group index in [0, groupCount).
// The mapping uses FNV-1a and is stable across processes and runs.
func GroupOf(key string, groupCount int) int {
	hash := fnv.New32a()
	hash.Write([]byte(key))
	return int(hash.Sum32() % uint32(groupCount))
}

// ownerOf returns the instance owning the given group when groupCount groups
// are split into parallelism contiguous ranges.
func ownerOf(group, groupCount, parallelism int) int {
	return group * parallelism / groupCount
}

// ownership returns the owning instance for every key group.
func ownership(groupCount, parallelism int) []int {
	owners := make([]int, groupCount)
	for group := 0; group < groupCount; group++ {
		owners[group] = ownerOf(group, groupCount, parallelism)
	}
	return owners
}

// ranges returns the inclusive [Start, End] key-group interval of every
// instance. The ranges are pairwise disjoint and exactly cover all groups.
func ranges(groupCount, parallelism int) []Range {
	result := make([]Range, 0, parallelism)
	start := 0
	for instance := 0; instance < parallelism; instance++ {
		// First group that maps to instance+1, i.e. the exclusive end.
		end := groupCount - 1
		if next := (instance + 1) * groupCount / parallelism; instance+1 < parallelism {
			end = next - 1
		}
		result = append(result, Range{Instance: instance, Start: start, End: end})
		start = end + 1
	}
	return result
}

func (r *Router) snapshotLocked(parallelism int) *Result {
	keys := make([]string, 0, len(r.values))
	for key := range r.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return &Result{
		OldParallelism: parallelism,
		NewParallelism: parallelism,
		Ranges:         ranges(r.groupCount, parallelism),
		Ownership:      ownership(r.groupCount, parallelism),
		Migrations:     []Migration{},
		Moves:          []Move{},
		MovedKeyNum:    0,
		States:         cloneValues(r.values, keys),
	}
}

func cloneValues(values map[string][]byte, orderedKeys []string) map[string][]byte {
	cloned := make(map[string][]byte, len(values))
	if orderedKeys != nil {
		for _, key := range orderedKeys {
			cloned[key] = bytes.Clone(values[key])
		}
		return cloned
	}
	for key, value := range values {
		cloned[key] = bytes.Clone(value)
	}
	return cloned
}
