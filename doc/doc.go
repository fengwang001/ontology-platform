// Package doc implements nested documents with int64/string/bool leaves and
// null-aware merge patches. It is metadata-agnostic: each leaf carries a
// caller-supplied metadata value of type M.
package doc

import "sort"

// ErrInvalid is returned for malformed devices, keys, patches and paths.
type ErrInvalid struct{ Msg string }

func (e ErrInvalid) Error() string { return "invalid: " + e.Msg }

// Value is a leaf value. Its concrete type is int64, string or bool.
type Value any

// Leaf is a document leaf with its metadata.
type Leaf[M any] struct {
	Value Value
	Meta  M
}

// Node is a document node: either an object (non-nil Map) or a leaf (nil Map).
type Node[M any] struct {
	Map  map[string]*Node[M]
	Leaf *Leaf[M]
}

// Kind reports whether a value has an allowed leaf type.
func Kind(v Value) bool {
	switch v.(type) {
	case int64, string, bool:
		return true
	default:
		return false
	}
}

// ValidKey reports whether k is 1..64 bytes and contains no '.'.
func ValidKey(k string) bool {
	n := len(k)
	if n < 1 || n > 64 {
		return false
	}
	for i := 0; i < n; i++ {
		if k[i] == '.' {
			return false
		}
	}
	return true
}

// ParsePatch validates a raw patch (root must be a non-empty object) and
// returns its normalized object form. Leaf values must be int64/string/bool;
// int is accepted and normalized to int64. Depth is judged by the patch shape
// alone: the deepest leaf/null must be at most 4 key segments from the root.
func ParsePatch(patch map[string]any) (map[string]any, error) {
	if patch == nil || len(patch) == 0 {
		return nil, ErrInvalid{Msg: "root patch must be a non-empty object"}
	}
	out := make(map[string]any, len(patch))
	if err := parseObj(patch, out, nil, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func parseObj(in, out map[string]any, segs []string, depth int) error {
	// Iterate in sorted order so errors are deterministic.
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rv := in[k]
		if !ValidKey(k) {
			return ErrInvalid{Msg: "invalid key: " + k}
		}
		path := append(append([]string(nil), segs...), k)
		switch v := rv.(type) {
		case nil:
			if depth+1 > 4 {
				return ErrInvalid{Msg: "path exceeds 4 segments"}
			}
			out[k] = nil
		case int:
			if depth+1 > 4 {
				return ErrInvalid{Msg: "path exceeds 4 segments"}
			}
			out[k] = int64(v)
		case int64, string, bool:
			if depth+1 > 4 {
				return ErrInvalid{Msg: "path exceeds 4 segments"}
			}
			out[k] = v
		case map[string]any:
			if depth+1 > 4 || (depth+1 >= 4 && len(v) > 0) {
				// This key already reaches the 4th segment; any child key
				// would occupy a 5th segment, so a non-empty object is illegal.
				return ErrInvalid{Msg: "path exceeds 4 segments"}
			}
			sub := make(map[string]any, len(v))
			if err := parseObj(v, sub, path, depth+1); err != nil {
				return err
			}
			out[k] = sub
		default:
			return ErrInvalid{Msg: "unsupported patch value"}
		}
	}
	return nil
}

// Event marks a leaf that a merge touched.
type Event[M any] struct {
	Path string
	Type EventType
	Old  *Leaf[M]
	New  *Leaf[M]
}

// EventType classifies a merge event.
type EventType int

const (
	// Added: the leaf did not exist before and exists after.
	Added EventType = iota
	// Removed: the leaf existed before and does not exist after.
	Removed
	// Changed: the same path holds a leaf whose value/type changed.
	Changed
)

// Report describes the outcome of a merge.
type Report[M any] struct {
	// Root is the merged root (always an object node).
	Root *Node[M]
	// Events lists every added/removed/changed leaf, sorted by path.
	Events []Event[M]
	// LeafCount is the number of leaves in Root.
	LeafCount int
	// touched counts document nodes actually visited/created/deleted;
	// the root and untouched shared subtrees are not counted.
	touched int
	// metaFn produces metadata for leaves created by this merge.
	metaFn func() M
}

// NewRoot returns an empty root object node.
func NewRoot[M any]() *Node[M] { return &Node[M]{Map: map[string]*Node[M]{}} }

// Merge merges the validated patch object into root with copy-on-write.
// newMeta produces metadata for leaves created by this merge.
func Merge[M any](root *Node[M], patch map[string]any, newMeta func() M) *Report[M] {
	r := &Report[M]{Root: root, metaFn: newMeta}
	if root == nil {
		root = NewRoot[M]()
	}
	r.Root = r.applyObj(root, patch, "")
	sortEvents(r.Events)
	r.LeafCount = countLeaves(r.Root)
	return r
}

// EqualLeaf reports whether two leaves have equal value and dynamic type.
func EqualLeaf(a, b Value) bool {
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	default:
		return false
	}
}

// LeafPath pairs a dotted path with its leaf.
type LeafPath[M any] struct {
	Path string
	Leaf *Leaf[M]
}

// Leaves returns every leaf path (in byte order) and its leaf.
func Leaves[M any](root *Node[M]) []LeafPath[M] {
	if root == nil {
		return nil
	}
	out := collectLeaves(root, "", nil)
	return out
}

func collectLeaves[M any](n *Node[M], prefix string, out []LeafPath[M]) []LeafPath[M] {
	if n.Leaf != nil {
		return append(out, LeafPath[M]{Path: prefix, Leaf: n.Leaf})
	}
	keys := make([]string, 0, len(n.Map))
	for k := range n.Map {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		out = collectLeaves(n.Map[k], p, out)
	}
	return out
}

func countLeaves[M any](n *Node[M]) int {
	if n == nil {
		return 0
	}
	if n.Leaf != nil {
		return 1
	}
	c := 0
	for _, ch := range n.Map {
		c += countLeaves(ch)
	}
	return c
}

// Find walks path segments and returns the node, or nil if absent.
func Find[M any](root *Node[M], segs []string) *Node[M] {
	n := root
	for _, seg := range segs {
		if n == nil || n.Map == nil {
			return nil
		}
		n = n.Map[seg]
	}
	return n
}

// CloneLeaf returns a pointer to a copy of l.
func CloneLeaf[M any](l *Leaf[M]) *Leaf[M] {
	return &Leaf[M]{Value: l.Value, Meta: l.Meta}
}

// applyObj merges patch (non-nil map) into n which must be an object node.
// The returned node is a new node whenever anything under it changed;
// untouched subtrees are shared with the original document. Empty objects are
// pruned by callers when the key disappears (applyVal returns nil).
func (r *Report[M]) applyObj(n *Node[M], patch map[string]any, prefix string) *Node[M] {
	if n == nil {
		n = NewRoot[M]()
	}
	out := n
	for k, pv := range patch {
		var child *Node[M]
		if n.Map != nil {
			child = n.Map[k]
		}
		merged := r.applyVal(joinPath(prefix, k), child, pv)
		if merged != child {
			if out == n {
				out = &Node[M]{Map: cloneMap(n.Map)}
			}
			if merged == nil {
				delete(out.Map, k)
			} else {
				out.Map[k] = merged
			}
		}
	}
	return out
}

// applyVal merges one patch value into child located at prefix. It returns nil
// when the key must be absent after the merge (deletion or empty-object prune).
func (r *Report[M]) applyVal(prefix string, child *Node[M], pv any) *Node[M] {
	switch v := pv.(type) {
	case nil:
		r.touched++ // the target node of this patch entry
		if child == nil {
			return nil
		}
		r.removeSubtree(prefix, child)
		return nil
	case map[string]any:
		r.touched++
		if child == nil {
			merged := r.applyObj(nil, v, prefix)
			if len(merged.Map) == 0 {
				return nil // absent key + {} creates nothing
			}
			return merged
		}
		if child.Leaf != nil {
			r.removeSubtree(prefix, child)
			merged := r.applyObj(nil, v, prefix)
			if len(merged.Map) == 0 {
				return nil // leaf + {} deletes the leaf
			}
			return merged
		}
		merged := r.applyObj(child, v, prefix)
		if len(merged.Map) == 0 {
			return nil // the existing object became empty after the merge
		}
		return merged
	default: // validated leaf value only
		r.touched++
		if child != nil && child.Leaf != nil {
			if EqualLeaf(child.Leaf.Value, v) {
				return child // identical value and type: mv preserved, no event
			}
			nl := &Leaf[M]{Value: v, Meta: r.metaFn()}
			r.Events = append(r.Events, Event[M]{
				Path: prefix, Type: Changed, Old: child.Leaf, New: nl,
			})
			return &Node[M]{Leaf: nl}
		}
		if child != nil {
			r.removeSubtree(prefix, child)
		}
		leaf := &Leaf[M]{Value: v, Meta: r.metaFn()}
		r.Events = append(r.Events, Event[M]{Path: prefix, Type: Added, New: leaf})
		return &Node[M]{Leaf: leaf}
	}
}

func (r *Report[M]) removeSubtree(prefix string, n *Node[M]) {
	if n.Leaf != nil {
		r.touched++
		r.Events = append(r.Events, Event[M]{Path: prefix, Type: Removed, Old: n.Leaf})
		return
	}
	keys := make([]string, 0, len(n.Map))
	for k := range n.Map {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.touched++
		r.removeSubtree(joinPath(prefix, k), n.Map[k])
	}
}

func (r *Report[M]) addSubtree(prefix string, n *Node[M]) {
	if n.Leaf != nil {
		r.touched++
		r.Events = append(r.Events, Event[M]{Path: prefix, Type: Added, New: n.Leaf})
		return
	}
	keys := make([]string, 0, len(n.Map))
	for k := range n.Map {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.touched++
		r.addSubtree(joinPath(prefix, k), n.Map[k])
	}
}

func joinPath(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

func cloneMap[M any](m map[string]*Node[M]) map[string]*Node[M] {
	out := make(map[string]*Node[M], len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortEvents[M any](evs []Event[M]) {
	sort.Slice(evs, func(i, j int) bool { return evs[i].Path < evs[j].Path })
}
