package encryption

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// captureLogger 收集日志，便于断言“对象、密文与判定依据”是否被打印。
type captureLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (l *captureLogger) Log(_ context.Context, level slog.Level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", level, msg)
	for i := 0; i+1 < len(args); i += 2 {
		fmt.Fprintf(&b, " %v=%v", args[i], args[i+1])
	}
	l.msgs = append(l.msgs, b.String())
}

func (l *captureLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.msgs, "\n")
}

func (l *captureLogger) containsAll(want ...string) bool {
	text := l.text()
	for _, w := range want {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func newTestStore(t *testing.T) (*Store, *KeyRing, *captureLogger) {
	t.Helper()
	ring := NewKeyRing()
	if _, err := ring.GenerateKey(); err != nil {
		t.Fatalf("generate initial key: %v", err)
	}
	logs := &captureLogger{}
	store := NewStore(NewCodec(ring), []string{"ssn", "email"}, logs)
	return store, ring, logs
}

func mustGetStored(t *testing.T, s *Store, id, attr string) []byte {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[id]
	if !ok {
		t.Fatalf("object %s missing", id)
	}
	return append([]byte(nil), obj.Attributes[attr]...)
}

// 1. 写入加密、读取解密；密文不泄露明文语义。
func TestPutEncryptsGetDecrypts(t *testing.T) {
	store, _, logs := newTestStore(t)
	ctx := context.Background()
	obj := Object{
		ID:         "user-1",
		Attributes: map[string][]byte{"ssn": []byte("110-SECRET-SSN"), "email": []byte("a@example.com"), "city": []byte("Shanghai")},
	}
	if err := store.Put(ctx, obj, false); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 落库内容必须是密文，且不包含明文子串。
	rawSSN := mustGetStored(t, store, "user-1", "ssn")
	rawCity := mustGetStored(t, store, "user-1", "city")
	if bytes.Contains(rawSSN, []byte("SECRET")) || bytes.Equal(rawSSN, obj.Attributes["ssn"]) {
		t.Fatalf("ssn stored without encryption: %q", rawSSN)
	}
	if !bytes.Equal(rawCity, obj.Attributes["city"]) {
		t.Fatalf("non-sensitive attribute should be stored as-is")
	}

	got, err := store.Get(ctx, "user-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got.Attributes["ssn"], obj.Attributes["ssn"]) {
		t.Fatalf("decrypt mismatch: got %q want %q", got.Attributes["ssn"], obj.Attributes["ssn"])
	}
	if !bytes.Equal(got.Attributes["city"], obj.Attributes["city"]) {
		t.Fatalf("plain attribute mismatch")
	}

	// 日志需包含对象 ID、密文与判定依据，且不得打印明文。
	logText := logs.text()
	if !logs.containsAll("object_id=user-1", "ciphertext=", "basis=", "nonce=", "key_version=1") {
		t.Fatalf("logs missing object/ciphertext/decision basis:\n%s", logText)
	}
	if strings.Contains(logText, "110-SECRET-SSN") {
		t.Fatalf("plaintext leaked into logs: %s", logText)
	}
	t.Logf("decision log:\n%s", logText)
}

// 2. 随机 nonce：相同明文每次产生不同密文；密文带密钥版本且均可解。
func TestRandomNonceMakesCiphertextUnique(t *testing.T) {
	ring := NewKeyRing()
	ver, err := ring.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	codec := NewCodec(ring)
	plaintext := []byte("repeatable-plaintext")

	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		ct1, err := codec.Seal(plaintext)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		ct2, err := codec.Seal(plaintext)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if bytes.Equal(ct1, ct2) {
			t.Fatalf("identical ciphertext for same plaintext (nonce not random)")
		}
		env1, err := EnvelopeOf(ct1)
		if err != nil {
			t.Fatalf("envelope parse: %v", err)
		}
		if env1.KeyVer != ver {
			t.Fatalf("ciphertext tagged with wrong key version: got %d want %d", env1.KeyVer, ver)
		}
		if _, dup := seen[string(env1.Nonce)]; dup {
			t.Fatalf("duplicate nonce detected")
		}
		seen[string(env1.Nonce)] = struct{}{}
		back, err := codec.Open(ct1)
		if err != nil || !bytes.Equal(back, plaintext) {
			t.Fatalf("open roundtrip: %v back=%q", err, back)
		}
	}
}

// 3. 查询/排序必须在解密后进行；直接对密文操作被拒绝。
func TestQueryAndSortAfterDecryption(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	records := []Object{
		{ID: "u1", Attributes: map[string][]byte{"email": []byte("c@x.com")}},
		{ID: "u2", Attributes: map[string][]byte{"email": []byte("a@x.com")}},
		{ID: "u3", Attributes: map[string][]byte{"email": []byte("b@x.com")}},
	}
	for _, rec := range records {
		if err := store.Put(ctx, rec, false); err != nil {
			t.Fatalf("put %s: %v", rec.ID, err)
		}
	}

	matched, err := store.Query(ctx, Predicate{Attribute: "email", Equals: []byte("a@x.com")})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(matched) != 1 || matched[0].ID != "u2" {
		t.Fatalf("decrypt-then-query returned %+v", matched)
	}

	sorted, err := store.SortAll(ctx, SortOrder{Attribute: "email"})
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	gotOrder := []string{sorted[0].ID, sorted[1].ID, sorted[2].ID}
	if strings.Join(gotOrder, ",") != "u2,u3,u1" {
		t.Fatalf("decrypt-then-sort order = %v", gotOrder)
	}

	if _, err := store.Query(ctx, Predicate{Attribute: "email", Equals: []byte("a@x.com"), OnCiphertext: true}); !errors.Is(err, ErrCiphertextOperation) {
		t.Fatalf("ciphertext query should be rejected, got %v", err)
	}
	if _, err := store.SortAll(ctx, SortOrder{Attribute: "email", OnCiphertext: true}); !errors.Is(err, ErrCiphertextOperation) {
		t.Fatalf("ciphertext sort should be rejected, got %v", err)
	}

	// 被拒绝的操作不改变数据。
	got, err := store.Get(ctx, "u2")
	if err != nil || !bytes.Equal(got.Attributes["email"], []byte("a@x.com")) {
		t.Fatalf("data mutated after rejected ciphertext operation: %v %v", got, err)
	}
}

// 直接对密文排序得到的序与明文序无关：随机 nonce 重加密若干次后，
// 密文字节序几乎必然与明文插入序不一致。
func TestCiphertextOrderDoesNotMatchPlaintext(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	values := [][]byte{[]byte("aaaa"), []byte("bbbb"), []byte("cccc"), []byte("dddd")}
	var ids []string
	for i, v := range values {
		id := fmt.Sprintf("o%d", i)
		ids = append(ids, id)
		if err := store.Put(ctx, Object{ID: id, Attributes: map[string][]byte{"ssn": v}}, false); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 10; attempt++ {
		var prev []byte
		diverged := false
		for _, id := range ids {
			ct := mustGetStored(t, store, id, "ssn")
			if prev != nil && bytes.Compare(prev, ct) > 0 {
				diverged = true
				break
			}
			prev = ct
		}
		if diverged {
			return
		}
		for _, id := range ids {
			obj, err := store.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Put(ctx, obj, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Fatalf("ciphertext ordering never diverged from plaintext order; random nonce not observable")
}

// 4. 密钥轮换：新版本加密，旧数据仍可解；旧密钥受保护不可删除。
func TestKeyRotationKeepsOldDataReadable(t *testing.T) {
	store, ring, _ := newTestStore(t)
	ctx := context.Background()
	before := Object{ID: "old", Attributes: map[string][]byte{"ssn": []byte("written-before-rotation")}}
	if err := store.Put(ctx, before, false); err != nil {
		t.Fatal(err)
	}
	oldEnv, err := EnvelopeOf(mustGetStored(t, store, "old", "ssn"))
	if err != nil {
		t.Fatal(err)
	}
	if oldEnv.KeyVer != 1 {
		t.Fatalf("expected version 1, got %d", oldEnv.KeyVer)
	}

	newVer, err := ring.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newVer != 2 || len(ring.Versions()) != 2 {
		t.Fatalf("rotation state wrong: current=%d versions=%v", newVer, ring.Versions())
	}

	// 旧数据在轮换后仍可用 v1 密钥解密。
	gotOld, err := store.Get(ctx, "old")
	if err != nil {
		t.Fatalf("old data unreadable after rotation: %v", err)
	}
	if !bytes.Equal(gotOld.Attributes["ssn"], before.Attributes["ssn"]) {
		t.Fatalf("old plaintext mismatch: %q", gotOld.Attributes["ssn"])
	}

	// 新数据使用新版本密钥加密。
	after := Object{ID: "new", Attributes: map[string][]byte{"ssn": []byte("written-after-rotation")}}
	if err := store.Put(ctx, after, false); err != nil {
		t.Fatal(err)
	}
	newEnv, err := EnvelopeOf(mustGetStored(t, store, "new", "ssn"))
	if err != nil {
		t.Fatal(err)
	}
	if newEnv.KeyVer != newVer {
		t.Fatalf("new write should use rotated key: got %d want %d", newEnv.KeyVer, newVer)
	}

	// 删除旧版本密钥必须被拒绝，且密钥环与数据不变。
	if err := ring.RetireKey(1); !errors.Is(err, ErrKeyRotationBreaksOldData) {
		t.Fatalf("retiring old key should be rejected, got %v", err)
	}
	if len(ring.Versions()) != 2 {
		t.Fatalf("rejected retirement mutated the key ring")
	}

	// Rewrap 用新密钥重加密旧数据，明文不变。
	n, err := store.Rewrap(ctx)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 re-wrapped attribute, got %d", n)
	}
	rewrappedEnv, err := EnvelopeOf(mustGetStored(t, store, "old", "ssn"))
	if err != nil {
		t.Fatal(err)
	}
	if rewrappedEnv.KeyVer != newVer {
		t.Fatalf("rewrap should tag new key version, got %d", rewrappedEnv.KeyVer)
	}
	gotRewrapped, err := store.Get(ctx, "old")
	if err != nil || !bytes.Equal(gotRewrapped.Attributes["ssn"], before.Attributes["ssn"]) {
		t.Fatalf("plaintext changed after rewrap: %v %v", gotRewrapped, err)
	}
}

// 缺失密钥版本时给出明确错误（模拟“旧数据不可解”的坏轮换会被识别）。
func TestOpenFailsWhenKeyVersionMissing(t *testing.T) {
	ring := NewKeyRing()
	if _, err := ring.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	codec := NewCodec(ring)
	ct, err := codec.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	emptyRing := NewKeyRing()
	if _, err := NewCodec(emptyRing).Open(ct); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

// 5. 确定性加密请求被拒绝，且不写入、不改变已有数据。
func TestDeterministicPutRejectedAndNoDataChange(t *testing.T) {
	store, _, logs := newTestStore(t)
	ctx := context.Background()
	original := Object{ID: "u1", Attributes: map[string][]byte{"ssn": []byte("keep-me")}}
	if err := store.Put(ctx, original, false); err != nil {
		t.Fatal(err)
	}
	before := mustGetStored(t, store, "u1", "ssn")

	rejected := Object{ID: "u1", Attributes: map[string][]byte{"ssn": []byte("overwrite-me")}}
	if err := store.Put(ctx, rejected, true); !errors.Is(err, ErrDeterministicEncryption) {
		t.Fatalf("expected ErrDeterministicEncryption, got %v", err)
	}

	after := mustGetStored(t, store, "u1", "ssn")
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected deterministic put mutated stored data")
	}
	got, err := store.Get(ctx, "u1")
	if err != nil || !bytes.Equal(got.Attributes["ssn"], []byte("keep-me")) {
		t.Fatalf("plaintext changed after rejected put: %v %v", got, err)
	}
	if !logs.containsAll("put rejected", "deterministic_encryption_forbidden", "decision=reject") {
		t.Fatalf("rejection log missing reason/basis:\n%s", logs.text())
	}

	newObj := Object{ID: "u2", Attributes: map[string][]byte{"ssn": []byte("never-written")}}
	if err := store.Put(ctx, newObj, true); !errors.Is(err, ErrDeterministicEncryption) {
		t.Fatalf("expected rejection for new object, got %v", err)
	}
	if _, err := store.Get(ctx, "u2"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("rejected put created data, got err=%v", err)
	}
}

// 6. 并发加解密同一明文：密文互不相同，但都可解为同一明文。
func TestConcurrentEncryptDecryptConsistency(t *testing.T) {
	ring := NewKeyRing()
	if _, err := ring.GenerateKey(); err != nil {
		t.Fatal(err)
	}
	codec := NewCodec(ring)
	plaintext := []byte("concurrent-secret-value")

	const goroutines = 32
	var wg sync.WaitGroup
	ciphertexts := make([][]byte, goroutines)
	errs := make(chan error, goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			ct, err := codec.Seal(plaintext)
			if err != nil {
				errs <- err
				return
			}
			ciphertexts[idx] = ct
			back, err := codec.Open(ct)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(back, plaintext) {
				errs <- fmt.Errorf("goroutine %d got %q", idx, back)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	seen := make(map[string]struct{}, goroutines)
	for _, ct := range ciphertexts {
		if _, dup := seen[string(ct)]; dup {
			t.Fatalf("two goroutines produced identical ciphertext")
		}
		seen[string(ct)] = struct{}{}
	}

	// 任一密文都可被独立解开：密文自描述密钥版本。
	for _, ct := range ciphertexts {
		back, err := codec.Open(ct)
		if err != nil || !bytes.Equal(back, plaintext) {
			t.Fatalf("cross decrypt failed: %v", err)
		}
	}
}

// 7. Store 层面的并发读写：数据一致、无竞态。
func TestConcurrentStorePutGet(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	const writers = 16
	const readers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, writers+readers)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			id := fmt.Sprintf("obj-%d", n)
			ssn := []byte(fmt.Sprintf("ssn-%05d", n))
			if err := store.Put(ctx, Object{ID: id, Attributes: map[string][]byte{"ssn": ssn}}, false); err != nil {
				errCh <- err
				return
			}
			got, err := store.Get(ctx, id)
			if err != nil || !bytes.Equal(got.Attributes["ssn"], ssn) {
				errCh <- fmt.Errorf("writer roundtrip mismatch: %v", err)
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < writers; k++ {
				id := fmt.Sprintf("obj-%d", k)
				want := []byte(fmt.Sprintf("ssn-%05d", k))
				got, err := store.Get(ctx, id)
				if errors.Is(err, ErrObjectNotFound) {
					continue // 写入尚未发生，允许
				}
				if err != nil || !bytes.Equal(got.Attributes["ssn"], want) {
					errCh <- fmt.Errorf("reader saw inconsistent data for %s: %v", id, err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	matched, err := store.Query(ctx, Predicate{Attribute: "ssn", Equals: []byte("ssn-00007")})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != "obj-7" {
		t.Fatalf("post-concurrency query mismatch: %+v", matched)
	}
}
