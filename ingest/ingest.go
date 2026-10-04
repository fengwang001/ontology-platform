package ingest

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/coerce"
	"ontology/mapping"
)

var ErrNotFound = errors.New("document not found")

var (
	ErrTypeConflict = coerce.ErrTypeConflict
	ErrStrict       = mapping.ErrStrict
	ErrFieldLimit   = mapping.ErrFieldLimit
)

type Store struct {
	mu   sync.RWMutex
	mp   *mapping.Mapping
	docs map[string]map[string]any
}

type IndexResult struct {
	Doc     map[string]any
	Ignored []string
	MV      int
}

func New(mode mapping.Mode, fmax int) (*Store, error) {
	mp, err := mapping.New(mode, fmax)
	if err != nil {
		return nil, err
	}
	return &Store{mp: mp, docs: map[string]map[string]any{}}, nil
}

func (s *Store) Index(id string, doc map[string]any) (res *IndexResult, err error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if err := validateValue(doc, 1); err != nil {
		return nil, err
	}
	tx := s.mp.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	out := map[string]any{}
	var ignored []string
	if err := s.walk(tx, nil, tx.Root(), doc, out, &ignored); err != nil {
		return nil, err
	}
	mv := tx.Commit()
	normalized := out
	s.mu.Lock()
	s.docs[id] = normalized
	s.mu.Unlock()
	sort.Strings(ignored)
	return &IndexResult{Doc: out, Ignored: ignored, MV: mv}, nil
}

func (s *Store) Get(id string) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.docs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return d, nil
}

func (s *Store) PutMapping(path []string, typ coerce.Type) error {
	return s.mp.PutMapping(path, typ)
}

func (s *Store) MV() int { return s.mp.MV() }

func (s *Store) Fields() map[string]coerce.Type { return s.mp.Snapshot() }

// walk traverses one object level. level is the matching field-tree level;
// out is the normalized object being built.
func (s *Store) walk(tx *mapping.Tx, path []string, level mapping.Level, in, out map[string]any, ignored *[]string) error {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		childPath := append(append([]string{}, path...), key)
		raw := in[key]
		typ, childLevel, exists := tx.Lookup(level, key)
		if !exists {
			switch tx.Mode() {
			case mapping.DynamicFalse:
				*ignored = append(*ignored, strings.Join(childPath, "."))
				continue
			case mapping.DynamicStrict:
				return strictErr(childPath)
			}
			if err := s.inferNew(tx, level, key, childPath, raw, out, ignored); err != nil {
				return err
			}
			continue
		}
		nv, keep, err := s.coerceExisting(tx, typ, childLevel, childPath, raw, ignored)
		if err != nil {
			return err
		}
		if keep {
			out[key] = nv
		}
	}
	return nil
}

// coerceExisting adapts an existing leaf/object field to a scalar, object,
// array or nil. keep=false means nil/empty-array: no mapping check, dropped.
func (s *Store) coerceExisting(tx *mapping.Tx, typ coerce.Type, childLevel mapping.Level, path []string, raw any, ignored *[]string) (any, bool, error) {
	switch v := raw.(type) {
	case nil:
		return nil, false, nil
	case []any:
		if len(v) == 0 {
			return nil, false, nil
		}
		arr := make([]any, 0, len(v))
		for _, el := range v {
			if el == nil {
				arr = append(arr, nil)
				continue
			}
			if typ == coerce.Object {
				return nil, false, conflict(path)
			}
			cv, err := coerce.Coerce(typ, el)
			if err != nil {
				return nil, false, conflict(path)
			}
			arr = append(arr, cv)
		}
		return arr, true, nil
	case map[string]any:
		if typ != coerce.Object {
			return nil, false, conflict(path)
		}
		child := map[string]any{}
		if err := s.walk(tx, path, childLevel, v, child, ignored); err != nil {
			return nil, false, err
		}
		return child, true, nil
	default:
		if typ == coerce.Object {
			return nil, false, conflict(path)
		}
		cv, err := coerce.Coerce(typ, v)
		if err != nil {
			return nil, false, conflict(path)
		}
		return cv, true, nil
	}
}

// inferNew creates fields for a path unseen so far. For arrays the type is
// fixed by the first non-nil element; later elements are coerced to it.
func (s *Store) inferNew(tx *mapping.Tx, level mapping.Level, key string, path []string, raw any, out map[string]any, ignored *[]string) error {
	switch v := raw.(type) {
	case nil:
		return nil
	case []any:
		if len(v) == 0 {
			return nil
		}
		var typ coerce.Type
		for _, el := range v {
			if el == nil {
				continue
			}
			t, ok := coerce.Infer(el)
			if !ok || t == coerce.Object {
				return conflict(path)
			}
			typ = t
			break
		}
		if typ == "" {
			return nil
		}
		if _, err := tx.Create(level, key, typ); err != nil {
			return limitErr(path)
		}
		arr := make([]any, 0, len(v))
		for _, el := range v {
			if el == nil {
				arr = append(arr, nil)
				continue
			}
			cv, err := coerce.Coerce(typ, el)
			if err != nil {
				return conflict(path)
			}
			arr = append(arr, cv)
		}
		out[key] = arr
		return nil
	case map[string]any:
		childLevel, err := tx.Create(level, key, coerce.Object)
		if err != nil {
			return limitErr(path)
		}
		child := map[string]any{}
		if err := s.walk(tx, path, childLevel, v, child, ignored); err != nil {
			return err
		}
		out[key] = child
		return nil
	default:
		typ, ok := coerce.Infer(v)
		if !ok {
			return conflict(path)
		}
		if _, err := tx.Create(level, key, typ); err != nil {
			return limitErr(path)
		}
		cv, err := coerce.Coerce(typ, v)
		if err != nil {
			return conflict(path)
		}
		out[key] = cv
		return nil
	}
}

func conflict(path []string) error {
	return fmt.Errorf("%w: %s", coerce.ErrTypeConflict, strings.Join(path, "."))
}

func strictErr(path []string) error {
	return fmt.Errorf("%w: %s", mapping.ErrStrict, strings.Join(path, "."))
}

func limitErr(path []string) error {
	return fmt.Errorf("%w: %s", mapping.ErrFieldLimit, strings.Join(path, "."))
}
