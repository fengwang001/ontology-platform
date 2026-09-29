package objectcrypto

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

// Logger receives every allow/deny decision together with the object id, the
// ciphertext involved and the reason for the decision. Plaintext is never
// logged.
type Logger interface {
	LogDecision(ctx context.Context, event DecisionEvent)
}

type DecisionEvent struct {
	Operation  string
	ObjectID   string
	Field      string
	Ciphertext []byte
	Reason     string
	Allowed    bool
}

// Object is the plaintext view handled by callers. The Store never persists it
// in this form.
type Object struct {
	ID         string
	Properties map[string]string
}

// StoredObject is the at-rest form: every property value is a self-describing
// ciphertext envelope carrying its key version.
type StoredObject struct {
	ID         string
	Properties map[string][]byte
}

// Store encrypts every property value on write and decrypts on read, query
// and sort. Ciphertext is never compared or ordered directly.
type Store struct {
	cipher *Cipher
	ring   *KeyRing
	logger Logger

	mu     sync.RWMutex
	record map[string]StoredObject
}

func NewStore(cipher *Cipher, ring *KeyRing, logger Logger) *Store {
	return &Store{cipher: cipher, ring: ring, logger: logger, record: make(map[string]StoredObject)}
}

func (s *Store) log(ctx context.Context, event DecisionEvent) {
	if s.logger != nil {
		s.logger.LogDecision(ctx, event)
	}
}

// Put encrypts every property under the active key version and stores only
// ciphertext. On any failure the stored object is left untouched.
func (s *Store) Put(ctx context.Context, obj Object) error {
	if obj.ID == "" {
		return errors.New("objectcrypto: object id is required")
	}
	encrypted := make(map[string][]byte, len(obj.Properties))
	for field, value := range obj.Properties {
		blob, err := s.cipher.Encrypt([]byte(value))
		if err != nil {
			s.log(ctx, DecisionEvent{
				Operation: "put", ObjectID: obj.ID, Field: field,
				Reason: "encryption failed: " + err.Error(), Allowed: false,
			})
			return err
		}
		encrypted[field] = blob
	}

	s.mu.Lock()
	s.record[obj.ID] = StoredObject{ID: obj.ID, Properties: encrypted}
	s.mu.Unlock()

	for field, blob := range encrypted {
		s.log(ctx, DecisionEvent{
			Operation: "put", ObjectID: obj.ID, Field: field, Ciphertext: append([]byte(nil), blob...),
			Reason: "encrypted with random nonce under active key version", Allowed: true,
		})
	}
	return nil
}

// Get decrypts a stored object back to its plaintext form.
func (s *Store) Get(ctx context.Context, id string) (Object, bool, error) {
	s.mu.RLock()
	stored, ok := s.record[id]
	s.mu.RUnlock()
	if !ok {
		return Object{}, false, nil
	}
	obj := Object{ID: stored.ID, Properties: make(map[string]string, len(stored.Properties))}
	for field, blob := range stored.Properties {
		value, err := s.cipher.Decrypt(blob)
		if err != nil {
			s.log(ctx, DecisionEvent{
				Operation: "get", ObjectID: id, Field: field,
				Ciphertext: append([]byte(nil), blob...),
				Reason:     "decryption failed: " + err.Error(), Allowed: false,
			})
			return Object{}, false, err
		}
		obj.Properties[field] = string(value)
		s.log(ctx, DecisionEvent{
			Operation: "get", ObjectID: id, Field: field,
			Ciphertext: append([]byte(nil), blob...),
			Reason:     "decrypted using key version recorded in ciphertext", Allowed: true,
		})
	}
	return obj, true, nil
}

// StoredCiphertext exposes the at-rest blob for a field (used to demonstrate
// that plaintext never touches storage and to support tests).
func (s *Store) StoredCiphertext(id, field string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored, ok := s.record[id]
	if !ok {
		return nil, false
	}
	blob, ok := stored.Properties[field]
	return append([]byte(nil), blob...), ok
}

// QueryByField decrypts every object and compares plaintext. Equality over
// random-nonce ciphertext would be meaningless, so comparison always happens
// after decryption.
func (s *Store) QueryByField(ctx context.Context, field, value string) ([]Object, error) {
	var matches []Object
	for _, id := range s.sortedIDs() {
		obj, _, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if obj.Properties[field] == value {
			matches = append(matches, obj)
		}
	}
	s.log(ctx, DecisionEvent{
		Operation: "query", Field: field,
		Reason: "all objects decrypted before plaintext equality comparison", Allowed: true,
	})
	return matches, nil
}

// SortByField decrypts every object and sorts on plaintext values.
func (s *Store) SortByField(ctx context.Context, field string) ([]Object, error) {
	objs := make([]Object, 0)
	for _, id := range s.sortedIDs() {
		obj, _, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
	}
	sort.SliceStable(objs, func(i, j int) bool {
		return objs[i].Properties[field] < objs[j].Properties[field]
	})
	s.log(ctx, DecisionEvent{
		Operation: "sort", Field: field,
		Reason: "all objects decrypted before plaintext ordering", Allowed: true,
	})
	return objs, nil
}

func (s *Store) sortedIDs() []string {
	s.mu.RLock()
	ids := make([]string, 0, len(s.record))
	for id := range s.record {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

// RequireDeterministicCiphertext models a caller demand that equal plaintext
// be stored as equal ciphertext. It is always rejected and changes nothing.
func (s *Store) RequireDeterministicCiphertext(ctx context.Context) error {
	err := ErrDeterministicCiphertext
	s.log(ctx, DecisionEvent{
		Operation: "require_deterministic_ciphertext",
		Reason:    "deterministic encryption would leak plaintext equality; random nonce is mandatory",
		Allowed:   false,
	})
	return err
}

// QueryCiphertext models an attempt to filter directly on a stored blob. It is
// always rejected because random nonces make ciphertext comparison meaningless
// and it would operate on leaked at-rest material; nothing is read or changed.
func (s *Store) QueryCiphertext(ctx context.Context, field string, blob []byte) error {
	err := ErrCiphertextQuery
	s.log(ctx, DecisionEvent{
		Operation: "query_ciphertext", Field: field,
		Ciphertext: append([]byte(nil), blob...),
		Reason:     "query/sort must run after decryption; ciphertext ordering or equality is forbidden",
		Allowed:    false,
	})
	return err
}

// RotateKeyUnreadable models a rotation that removes/replaces the old key
// version. It is always rejected and the key ring is never modified, so data
// encrypted under prior versions stays decryptable.
func (s *Store) RotateKeyUnreadable(ctx context.Context, version int) error {
	err := s.ring.Retire(version)
	s.log(ctx, DecisionEvent{
		Operation: "rotate_key_unreadable",
		Reason:    "retiring/replacing key version would strand existing ciphertext; all versions must be retained",
		Allowed:   false,
	})
	return err
}

// IsCiphertext reports whether value looks like an envelope produced by this
// package; useful for guards that reject raw ciphertext handling.
func IsCiphertext(value []byte) bool {
	return strings.HasPrefix(string(value), ciphertextPrefix)
}
