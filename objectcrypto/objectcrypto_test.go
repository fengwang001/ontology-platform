package objectcrypto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type captureLogger struct {
	mu     sync.Mutex
	events []DecisionEvent
}

func (l *captureLogger) LogDecision(_ context.Context, e DecisionEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *captureLogger) snapshot() []DecisionEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionEvent, len(l.events))
	copy(out, l.events)
	return out
}

func newTestStore(t *testing.T) (*Store, *KeyRing, *captureLogger) {
	t.Helper()
	ring := NewKeyRing()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if err := ring.Rotate(1, key); err != nil {
		t.Fatalf("initial key: %v", err)
	}
	logger := &captureLogger{}
	return NewStore(NewCipher(ring), ring, logger), ring, logger
}

func TestPutEncryptsGetDecrypts(t *testing.T) {
	ctx := context.Background()
	store, _, logger := newTestStore(t)
	obj := Object{ID: "user-1", Properties: map[string]string{
		"ssn":   "110-102-1999",
		"email": "ada@example.com",
	}}
	if err := store.Put(ctx, obj); err != nil {
		t.Fatalf("put: %v", err)
	}

	// At rest: ciphertext envelopes, never plaintext.
	ssnBlob, ok := store.StoredCiphertext("user-1", "ssn")
	if !ok {
		t.Fatal("stored ssn missing")
	}
	if bytes.Contains(ssnBlob, []byte("110-102-1999")) || !IsCiphertext(ssnBlob) {
		t.Fatalf("plaintext leaked at rest: %q", ssnBlob)
	}
	if v, err := KeyVersionOf(ssnBlob); err != nil || v != 1 {
		t.Fatalf("ciphertext must carry key version 1, got %d err=%v", v, err)
	}

	got, ok, err := store.Get(ctx, "user-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Properties["ssn"] != "110-102-1999" || got.Properties["email"] != "ada@example.com" {
		t.Fatalf("decrypted mismatch: %#v", got.Properties)
	}

	// Logs record object, ciphertext and reason.
	saw := false
	for _, e := range logger.snapshot() {
		if e.Operation == "put" && e.Field == "ssn" && e.Allowed &&
			bytes.Equal(e.Ciphertext, ssnBlob) &&
			strings.Contains(e.Reason, "random nonce") {
			saw = true
		}
	}
	if !saw {
		t.Fatal("expected allow log with object, ciphertext and random-nonce reason")
	}
}

func TestRandomNonceProducesDifferentCiphertext(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	const n = 32
	blobs := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("obj-%d", i)
		if err := store.Put(ctx, Object{ID: id, Properties: map[string]string{"secret": "same-secret"}}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		blob, _ := store.StoredCiphertext(id, "secret")
		blobs[string(blob)] = struct{}{}
		// Every blob stays decryptable to the same plaintext.
		got, _, err := store.Get(ctx, id)
		if err != nil || got.Properties["secret"] != "same-secret" {
			t.Fatalf("decrypt %d: %v", i, err)
		}
	}
	if len(blobs) != n {
		t.Fatalf("expected %d distinct ciphertexts for identical plaintext, got %d", n, len(blobs))
	}
}

func TestQueryAndSortAfterDecryption(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	data := []Object{
		{ID: "a", Properties: map[string]string{"city": "Shanghai"}},
		{ID: "b", Properties: map[string]string{"city": "Beijing"}},
		{ID: "c", Properties: map[string]string{"city": "Beijing"}},
	}
	for _, obj := range data {
		if err := store.Put(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := store.QueryByField(ctx, "city", "Beijing")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Properties["city"] != "Beijing" {
		t.Fatalf("query after decrypt mismatch: %#v", matches)
	}
	sorted, err := store.SortByField(ctx, "city")
	if err != nil {
		t.Fatal(err)
	}
	if sorted[0].Properties["city"] != "Beijing" || sorted[2].Properties["city"] != "Shanghai" {
		t.Fatalf("sort after decrypt mismatch: %#v", sorted)
	}
}

func TestKeyRotationKeepsOldDataReadable(t *testing.T) {
	ctx := context.Background()
	store, ring, _ := newTestStore(t)
	if err := store.Put(ctx, Object{ID: "old", Properties: map[string]string{"secret": "v1-data"}}); err != nil {
		t.Fatal(err)
	}
	oldBlob, _ := store.StoredCiphertext("old", "secret")
	if v, _ := KeyVersionOf(oldBlob); v != 1 {
		t.Fatalf("expected v1 ciphertext, got %d", v)
	}

	newKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := ring.Rotate(2, newKey); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Old ciphertext still decrypts with retained v1 key.
	old, ok, err := store.Get(ctx, "old")
	if err != nil || !ok || old.Properties["secret"] != "v1-data" {
		t.Fatalf("old data unreadable after rotation: ok=%v err=%v", ok, err)
	}

	// New writes use v2.
	if err := store.Put(ctx, Object{ID: "new", Properties: map[string]string{"secret": "v2-data"}}); err != nil {
		t.Fatal(err)
	}
	newBlob, _ := store.StoredCiphertext("new", "secret")
	if v, _ := KeyVersionOf(newBlob); v != 2 {
		t.Fatalf("expected v2 ciphertext, got %d", v)
	}
	if got, _, err := store.Get(ctx, "new"); err != nil || got.Properties["secret"] != "v2-data" {
		t.Fatalf("new data mismatch: %v", err)
	}
	if len(ring.Versions()) != 2 {
		t.Fatalf("both key versions must be retained, got %v", ring.Versions())
	}
}

func TestRejectionsAreDistinguishableAndDoNotMutate(t *testing.T) {
	ctx := context.Background()
	store, ring, logger := newTestStore(t)
	obj := Object{ID: "u", Properties: map[string]string{"secret": "topsecret"}}
	if err := store.Put(ctx, obj); err != nil {
		t.Fatal(err)
	}
	blob, _ := store.StoredCiphertext("u", "secret")

	// 1) deterministic ciphertext demand rejected.
	if err := store.RequireDeterministicCiphertext(ctx); !errors.Is(err, ErrDeterministicCiphertext) {
		t.Fatalf("want ErrDeterministicCiphertext, got %v", err)
	}
	// 2) direct ciphertext query rejected.
	if err := store.QueryCiphertext(ctx, "secret", blob); !errors.Is(err, ErrCiphertextQuery) {
		t.Fatalf("want ErrCiphertextQuery, got %v", err)
	}
	// 3) key retirement / strand-old-data rotation rejected.
	if err := store.RotateKeyUnreadable(ctx, 1); !errors.Is(err, ErrKeyRotatedUnreadable) {
		t.Fatalf("want ErrKeyRotatedUnreadable, got %v", err)
	}

	// The three reasons must be distinguishable.
	if errors.Is(ErrDeterministicCiphertext, ErrCiphertextQuery) ||
		errors.Is(ErrCiphertextQuery, ErrKeyRotatedUnreadable) ||
		errors.Is(ErrDeterministicCiphertext, ErrKeyRotatedUnreadable) {
		t.Fatal("rejection errors must be distinct")
	}

	// Nothing changed: key still present, data still decrypts, blob identical.
	if _, ok := ring.get(1); !ok {
		t.Fatal("rejected retirement must not remove the key")
	}
	blobAfter, _ := store.StoredCiphertext("u", "secret")
	if !bytes.Equal(blob, blobAfter) {
		t.Fatal("rejected operations must not mutate stored data")
	}
	if got, _, err := store.Get(ctx, "u"); err != nil || got.Properties["secret"] != "topsecret" {
		t.Fatalf("data altered by rejected operation: %v", err)
	}

	denied := 0
	for _, e := range logger.snapshot() {
		if !e.Allowed {
			denied++
			if e.Reason == "" {
				t.Fatal("deny decision must log its reason")
			}
		}
	}
	if denied != 3 {
		t.Fatalf("want 3 deny log entries, got %d", denied)
	}
}

func TestUnknownKeyVersionRejected(t *testing.T) {
	_, ring, _ := newTestStore(t)
	c := NewCipher(ring)
	blob, err := c.Encrypt([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	// Build a brand-new ring without the version used by the blob.
	empty := NewKeyRing()
	if _, err := NewCipher(empty).Decrypt(blob); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("want ErrUnknownKeyVersion, got %v", err)
	}
}

func TestConcurrentEncryptDecryptConsistency(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newTestStore(t)

	// Concurrent writes of the same object: every completed write must be
	// readable back consistently (final plaintext equals what was written).
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Put(ctx, Object{ID: "shared", Properties: map[string]string{"k": "v"}}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Concurrent decrypt of the same object and concurrent encryption of the
	// same plaintext must always round-trip.
	const readers = 32
	var rwg sync.WaitGroup
	for i := 0; i < readers; i++ {
		i := i
		rwg.Add(2)
		go func() {
			defer rwg.Done()
			obj, ok, err := store.Get(ctx, "shared")
			if err != nil || !ok || obj.Properties["k"] != "v" {
				t.Errorf("concurrent get: ok=%v err=%v obj=%#v", ok, err, obj)
			}
		}()
		go func() {
			defer rwg.Done()
			if err := store.Put(ctx, Object{ID: fmt.Sprintf("c-%d", i), Properties: map[string]string{"k": "v"}}); err != nil {
				t.Errorf("concurrent put: %v", err)
			}
		}()
	}
	rwg.Wait()
}

func TestSameKeySamePlaintextYieldsDistinctDecryptableBlobs(t *testing.T) {
	store, _, _ := newTestStore(t)
	c := store.cipher
	const n = 64
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		blob, err := c.Encrypt([]byte("deterministic-input"))
		if err != nil {
			t.Fatal(err)
		}
		seen[string(blob)] = struct{}{}
		plain, err := c.Decrypt(blob)
		if err != nil || string(plain) != "deterministic-input" {
			t.Fatalf("roundtrip %d: %v %q", i, err, plain)
		}
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct blobs, got %d", n, len(seen))
	}
}
