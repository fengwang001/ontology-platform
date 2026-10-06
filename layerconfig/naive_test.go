package layerconfig_test

import (
	"fmt"
	"sort"

	"ontology/layerconfig"
)

// naiveStore is an intentionally simple, independently written reference
// model. It copies the whole configuration on every publish (no sharing) and
// performs no persistence tricks. The random differential test asserts the
// optimized implementation is operationally identical to it.
type naiveStore struct {
	schemas map[string]layerconfig.Schema
	// versions[v] is a full snapshot. version 0 is the empty snapshot.
	versions []*naiveSnapshot
	current  int
}

type naiveEntry struct {
	kind  byte // 'v' value, 'c' cancel
	value layerconfig.Value
}

type naiveSnapshot struct {
	writes map[string]naiveEntry // scopedKey
	locks  map[string]bool
}

func newNaive() *naiveStore {
	s := &naiveStore{
		schemas: map[string]layerconfig.Schema{},
		current: 0,
	}
	s.versions = []*naiveSnapshot{newNaiveSnapshot()}
	return s
}

func newNaiveSnapshot() *naiveSnapshot {
	return &naiveSnapshot{writes: map[string]naiveEntry{}, locks: map[string]bool{}}
}

func (n *naiveStore) cloneSnap(src *naiveSnapshot) *naiveSnapshot {
	dst := newNaiveSnapshot()
	for k, v := range src.writes {
		// Deep-copy lists.
		if len(v.value.List) > 0 {
			cp := make([]string, len(v.value.List))
			copy(cp, v.value.List)
			v.value.List = cp
		}
		dst.writes[k] = v
	}
	for k := range src.locks {
		dst.locks[k] = true
	}
	return dst
}

func (n *naiveStore) register(sc layerconfig.Schema) error {
	if err := naiveValidateSchema(sc); err != nil {
		return err
	}
	n.schemas[sc.Key] = sc
	return nil
}

func naiveValidateSchema(sc layerconfig.Schema) error {
	if sc.Key == "" {
		return mkErr(layerconfig.KindInvalidArgument, "empty key")
	}
	switch sc.Type {
	case layerconfig.TypeString, layerconfig.TypeInt, layerconfig.TypeBool, layerconfig.TypeStringList:
	default:
		return mkErr(layerconfig.KindInvalidArgument, "bad type")
	}
	switch sc.Merge {
	case layerconfig.MergeOverride:
	case layerconfig.MergeAppend:
		if sc.Type != layerconfig.TypeStringList {
			return mkErr(layerconfig.KindInvalidArgument, "append only lists")
		}
	default:
		return mkErr(layerconfig.KindInvalidArgument, "bad merge")
	}
	if sc.Type == layerconfig.TypeInt && sc.Min > sc.Max {
		return mkErr(layerconfig.KindInvalidArgument, "bad range")
	}
	return nil
}

func mkErr(kind layerconfig.ErrorKind, msg string) error {
	return &layerconfig.Error{Kind: kind, Detail: msg}
}

func scopeValid(s layerconfig.Scope) (layerconfig.Layer, bool) {
	switch {
	case s.Env == "" && s.Region == "" && s.Instance == "":
		return layerconfig.LayerGlobal, true
	case s.Env != "" && s.Region == "" && s.Instance == "":
		return layerconfig.LayerEnv, true
	case s.Env != "" && s.Region != "" && s.Instance == "":
		return layerconfig.LayerRegion, true
	case s.Env != "" && s.Region != "" && s.Instance != "":
		return layerconfig.LayerInstance, true
	}
	return 0, false
}

func flatKey(s layerconfig.Scope, key string) string {
	return s.Env + "\x00" + s.Region + "\x00" + s.Instance + "\x00" + key
}

func chainOf(s layerconfig.Scope) []layerconfig.Scope {
	layer, _ := scopeValid(s)
	out := []layerconfig.Scope{{}}
	if layer >= layerconfig.LayerEnv {
		out = append(out, layerconfig.Scope{Env: s.Env})
	}
	if layer >= layerconfig.LayerRegion {
		out = append(out, layerconfig.Scope{Env: s.Env, Region: s.Region})
	}
	if layer >= layerconfig.LayerInstance {
		out = append(out, s)
	}
	return out
}

func (n *naiveStore) publish(changes []layerconfig.Change) (int, error) {
	if len(changes) == 0 {
		return n.current, mkErr(layerconfig.KindInvalidArgument, "empty batch")
	}
	layers := make([]layerconfig.Layer, len(changes))
	for i, c := range changes {
		if c.Key == "" {
			return n.current, mkErr(layerconfig.KindInvalidArgument, "empty key")
		}
		l, ok := scopeValid(c.Scope)
		if !ok {
			return n.current, mkErr(layerconfig.KindInvalidArgument, "bad scope")
		}
		if c.Op < layerconfig.OpWrite || c.Op > layerconfig.OpUnlock {
			return n.current, mkErr(layerconfig.KindInvalidArgument, "bad op")
		}
		layers[i] = l
	}

	schemas := map[string]layerconfig.Schema{}
	for _, c := range changes {
		sc, ok := n.schemas[c.Key]
		if !ok {
			return n.current, mkErr(layerconfig.KindKeyNotRegistered, c.Key)
		}
		schemas[c.Key] = sc
		if c.Op == layerconfig.OpWrite {
			if sc.Type == layerconfig.TypeInt && (c.Value.Int < sc.Min || c.Value.Int > sc.Max) {
				return n.current, mkErr(layerconfig.KindTypeOrRange, "range")
			}
		}
	}

	type tgt struct {
		s layerconfig.Scope
		k string
	}
	seen := map[tgt]int{}
	for i, c := range changes {
		t := tgt{c.Scope, c.Key}
		if first, dup := seen[t]; dup {
			return n.current, mkErr(layerconfig.KindBatchConflict,
				fmt.Sprintf("%d/%d", first, i))
		}
		seen[t] = i
	}

	base := n.versions[n.current]
	snap := n.cloneSnap(base)

	for i, c := range changes {
		_ = i
		sc := schemas[c.Key]
		fk := flatKey(c.Scope, c.Key)
		v := c.Value
		if len(v.List) > 0 {
			cp := make([]string, len(v.List))
			copy(cp, v.List)
			v.List = cp
		}
		switch c.Op {
		case layerconfig.OpWrite:
			snap.writes[fk] = naiveEntry{kind: 'v', value: v}
		case layerconfig.OpCancel:
			snap.writes[fk] = naiveEntry{kind: 'c'}
		case layerconfig.OpClear:
			delete(snap.writes, fk)
		case layerconfig.OpLock:
			snap.locks[fk] = true
		case layerconfig.OpUnlock:
			delete(snap.locks, fk)
		}
		_ = sc
	}

	for _, c := range changes {
		if c.Op != layerconfig.OpWrite && c.Op != layerconfig.OpCancel {
			continue
		}
		for _, bs := range chainOf(c.Scope)[:len(chainOf(c.Scope))-1] {
			if snap.locks[flatKey(bs, c.Key)] {
				return n.current, mkErr(layerconfig.KindLockConflict, "narrow under lock")
			}
		}
	}

	for _, c := range changes {
		if c.Op != layerconfig.OpLock {
			continue
		}
		bl, _ := scopeValid(c.Scope)
		for fk := range snap.writes {
			sc, key, ok := parseFlat(fk)
			if !ok || key != c.Key {
				continue
			}
			nl, _ := scopeValid(sc)
			if nl <= bl {
				continue
			}
			if bl >= layerconfig.LayerEnv && sc.Env != c.Scope.Env {
				continue
			}
			if bl >= layerconfig.LayerRegion && sc.Region != c.Scope.Region {
				continue
			}
			return n.current, mkErr(layerconfig.KindLockConflict, "narrower exists")
		}
	}

	// Required validation over all surviving identities.
	if err := n.required(snap); err != nil {
		return n.current, err
	}

	n.current++
	n.versions = append(n.versions, snap)
	return n.current, nil
}

func parseFlat(fk string) (layerconfig.Scope, string, bool) {
	var parts []string
	start := 0
	for i := 0; i < len(fk); i++ {
		if fk[i] == 0 && len(parts) < 3 {
			parts = append(parts, fk[start:i])
			start = i + 1
			if len(parts) == 3 {
				rest := fk[start:]
				sc := layerconfig.Scope{Env: parts[0], Region: parts[1], Instance: parts[2]}
				if _, ok := scopeValid(sc); !ok || rest == "" {
					return layerconfig.Scope{}, "", false
				}
				return sc, rest, true
			}
		}
	}
	return layerconfig.Scope{}, "", false
}

func (n *naiveStore) required(snap *naiveSnapshot) error {
	type id struct {
		s layerconfig.Scope
		l layerconfig.Layer
	}
	var ids []id
	seen := map[string]bool{}
	for fk := range snap.writes {
		sc, _, ok := parseFlat(fk)
		if !ok {
			continue
		}
		l, _ := scopeValid(sc)
		var chain []layerconfig.Scope
		if l >= layerconfig.LayerEnv {
			chain = append(chain, layerconfig.Scope{Env: sc.Env})
		}
		if l >= layerconfig.LayerRegion {
			chain = append(chain, layerconfig.Scope{Env: sc.Env, Region: sc.Region})
		}
		if l >= layerconfig.LayerInstance {
			chain = append(chain, sc)
		}
		for _, c := range chain {
			kk := flatKey(c, "")
			if !seen[kk] {
				seen[kk] = true
				ll, _ := scopeValid(c)
				ids = append(ids, id{c, ll})
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := ids[i], ids[j]
		if a.s.Env != b.s.Env {
			return a.s.Env < b.s.Env
		}
		if a.s.Region != b.s.Region {
			return a.s.Region < b.s.Region
		}
		return a.s.Instance < b.s.Instance
	})
	var reqKeys []string
	for k, sc := range n.schemas {
		if sc.Required {
			reqKeys = append(reqKeys, k)
		}
	}
	sort.Strings(reqKeys)
	for _, ident := range ids {
		for _, k := range reqKeys {
			r := n.resolveAt(snap, ident.s, k)
			if !r.Present {
				return mkErr(layerconfig.KindRequiredMissing, k)
			}
		}
	}
	return nil
}

// resolveAt computes resolution against an arbitrary snapshot using the
// current (latest) schemas, exactly like the real store.
func (n *naiveStore) resolveAt(snap *naiveSnapshot, target layerconfig.Scope, key string) layerconfig.Resolved {
	sc, ok := n.schemas[key]
	if !ok {
		return layerconfig.Unset
	}
	var (
		present bool
		scalar  layerconfig.Value
		list    []string
		dedup   map[string]struct{}
	)
	appendList := func(part []string) {
		for _, item := range part {
			if dedup == nil {
				dedup = map[string]struct{}{}
			}
			if _, dup := dedup[item]; dup {
				continue
			}
			dedup[item] = struct{}{}
			list = append(list, item)
		}
	}
	for _, scp := range chainOf(target) {
		e, ok := snap.writes[flatKey(scp, key)]
		if !ok {
			continue
		}
		if e.kind == 'c' {
			present = false
			scalar = layerconfig.Value{}
			list = nil
			dedup = nil
			continue
		}
		if sc.Merge == layerconfig.MergeAppend {
			present = true
			appendList(e.value.List)
		} else {
			present = true
			scalar = e.value
			list = nil
			dedup = nil
		}
	}
	if !present {
		return layerconfig.Unset
	}
	if sc.Merge == layerconfig.MergeAppend {
		if list == nil {
			list = []string{}
		}
		return layerconfig.Resolved{Present: true, Value: layerconfig.Value{List: list}}
	}
	return layerconfig.Resolved{Present: true, Value: scalar}
}

func (n *naiveStore) resolve(version int, target layerconfig.Scope, key string) layerconfig.Resolved {
	if version < 0 {
		version = n.current
	}
	if version == 0 {
		version = 0
	}
	return n.resolveAt(n.versions[version], target, key)
}

func (n *naiveStore) rollback(target int) (int, error) {
	if target < 0 {
		return n.current, mkErr(layerconfig.KindInvalidArgument, "neg")
	}
	if target > n.current {
		return n.current, mkErr(layerconfig.KindVersionNotFound, "missing")
	}
	if target == n.current {
		return n.current, nil
	}
	clone := n.cloneSnap(n.versions[target])
	if err := n.required(clone); err != nil {
		return n.current, err
	}
	n.current++
	n.versions = append(n.versions, clone)
	return n.current, nil
}
