package store

import (
	"errors"

	"ontology/version"
)

var errCrashed = errors.New("store: simulated crash")

// write appends one version through the staged protocol:
//
//  1. journal the version payload (durable body);
//  2. create the volatile version node (still unlinked)   [crash point];
//  3. link it into the key's chain / index                [crash point];
//  4. (commit happens separately, flipping visibility)     [crash point].
//
// All limit checks happen before ANY state change, so a rejection leaves
// chains, watermark and counters untouched.
func (s *Store) write(v *View, key string, kind version.Kind, value []byte) error {
	if v.txn == nil || v.txn.done {
		return errors.New("store: write on a finished/read-only view")
	}
	t := v.txn

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return errors.New("store: crashed; Recover required")
	}

	// --- limit checks first, no mutation below on failure ---
	ch := s.chains[key]
	if s.cfg.MaxVersionsPerKey > 0 {
		cur := 0
		if ch != nil {
			cur = ch.Len()
		}
		if cur >= s.cfg.MaxVersionsPerKey {
			return ErrVersionLimitForKey
		}
	}
	if s.cfg.MaxTotalVersions > 0 && s.total >= s.cfg.MaxTotalVersions {
		return ErrTotalVersionLimit
	}

	// Stage 1: durable payload.
	rec := writeRec{Key: key, Op: opPut, Value: append([]byte(nil), value...)}
	if kind == version.KindDelete {
		rec.Op = opDelete
		rec.Value = nil
	}
	s.journal.AppendWrite(t.id, rec)

	// Stage 2: create the volatile version body.
	if s.hook != nil && s.hook(t.id, CpAfterVersion) {
		s.crashLocked()
		return errCrashed
	}

	// Stage 3: link the node into the key's chain (index publication).
	ch = s.chains[key]
	if ch == nil {
		ch = &version.Chain{}
		s.chains[key] = ch
	}
	if _, ok := ch.Append(t.id, kind, rec.Value, 0); !ok {
		return ErrVersionLimitForKey
	}
	s.total++
	if s.hook != nil && s.hook(t.id, CpAfterIndex) {
		s.crashLocked()
		return errCrashed
	}

	t.keys[key] = struct{}{}
	return nil
}
