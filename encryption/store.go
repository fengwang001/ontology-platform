package encryption

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Object 是一条带主键的记录，敏感字段以密文形式存于 map 中。
type Object struct {
	ID         string
	Attributes map[string][]byte
}

// Predicate 描述一次解密后的查询条件。
type Predicate struct {
	Attribute string
	Equals    []byte
	// OnCiphertext 为 true 表示调用方要求直接对密文比较，
	// 这是被禁止的用法，Store 必须返回 ErrCiphertextOperation。
	OnCiphertext bool
}

// SortOrder 描述一次解密后的排序。
type SortOrder struct {
	Attribute string
	Desc      bool
	// OnCiphertext 为 true 表示调用方要求直接按密文排序，
	// 这是被禁止的用法，Store 必须返回 ErrCiphertextOperation。
	OnCiphertext bool
}

// Logger 是组件依赖的最小日志接口。
type Logger interface {
	Log(ctx context.Context, level slog.Level, msg string, args ...any)
}

// Store 是字段级加密的对象存储：敏感属性以密文落库，
// 读取/查询/排序时解密后进行。
type Store struct {
	mu        sync.RWMutex
	codec     *Codec
	sensitive map[string]struct{}
	objects   map[string]*Object
	logger    Logger
}

// NewStore 创建加密存储。sensitive 列出需要加密的属性名。
func NewStore(codec *Codec, sensitive []string, logger Logger) *Store {
	if codec == nil {
		panic("encryption: NewStore requires a non-nil codec")
	}
	sens := make(map[string]struct{}, len(sensitive))
	for _, name := range sensitive {
		sens[name] = struct{}{}
	}
	return &Store{
		codec:     codec,
		sensitive: sens,
		objects:   make(map[string]*Object),
		logger:    logger,
	}
}

// Put 写入对象：敏感属性加密后存储。deterministic=true
// 的确定性加密请求必须被拒绝，且不得改变已有数据。
func (s *Store) Put(ctx context.Context, obj Object, deterministic bool) error {
	if deterministic {
		// 拒绝确定性加密：相同明文必须产生不同密文。
		// 先拒绝后写入，保证被拒绝的操作不改变任何数据。
		s.log(ctx, slog.LevelWarn, "put rejected",
			"object_id", obj.ID,
			"reason", "deterministic_encryption_forbidden",
			"decision", "reject",
			"basis", "identical plaintext must yield distinct ciphertext via random unique nonce",
		)
		return ErrDeterministicEncryption
	}

	stored := &Object{ID: obj.ID, Attributes: make(map[string][]byte, len(obj.Attributes))}
	for attr, value := range obj.Attributes {
		if _, secret := s.sensitive[attr]; !secret {
			stored.Attributes[attr] = append([]byte(nil), value...)
			continue
		}
		sealed, err := s.codec.Seal(value)
		if err != nil {
			s.log(ctx, slog.LevelError, "encryption failed",
				"object_id", obj.ID, "attribute", attr, "decision", "abort",
				"basis", "seal returned error; no data written", "error", err.Error(),
			)
			return err
		}
		stored.Attributes[attr] = sealed
	}

	s.mu.Lock()
	s.objects[obj.ID] = stored
	s.mu.Unlock()

	for attr, value := range stored.Attributes {
		level := slog.LevelInfo
		secret, basis := false, "plaintext attribute"
		if _, ok := s.sensitive[attr]; ok {
			secret, basis = true, "stored as AES-256-GCM envelope with random unique nonce and key version"
		}
		s.log(ctx, level, "object stored",
			"object_id", obj.ID,
			"attribute", attr,
			"sensitive", secret,
			"ciphertext", summarize(value),
			"key_version", s.keyVersionOf(value, secret),
			"nonce", s.nonceOf(value, secret),
			"decision", "accept",
			"basis", basis,
		)
	}
	return nil
}

// Get 读取对象：敏感属性解密后返回明文。
func (s *Store) Get(ctx context.Context, id string) (Object, error) {
	s.mu.RLock()
	stored, ok := s.objects[id]
	s.mu.RUnlock()
	if !ok {
		return Object{}, ErrObjectNotFound
	}
	out := Object{ID: stored.ID, Attributes: make(map[string][]byte, len(stored.Attributes))}
	for attr, value := range stored.Attributes {
		if _, secret := s.sensitive[attr]; !secret {
			out.Attributes[attr] = append([]byte(nil), value...)
			continue
		}
		plain, err := s.codec.Open(value)
		if err != nil {
			return Object{}, fmt.Errorf("decrypt %s.%s: %w", id, attr, err)
		}
		out.Attributes[attr] = plain
	}
	s.log(ctx, slog.LevelInfo, "object read",
		"object_id", id,
		"ciphertext", s.ciphertextSummary(stored),
		"decision", "decrypt_then_return",
		"basis", "sensitive attributes are decrypted with the key version tagged in the envelope",
	)
	return out, nil
}

// Query 解密后按谓词过滤，返回匹配对象的明文副本。
func (s *Store) Query(ctx context.Context, p Predicate) ([]Object, error) {
	if _, secret := s.sensitive[p.Attribute]; secret && p.OnCiphertext {
		s.log(ctx, slog.LevelWarn, "query rejected",
			"attribute", p.Attribute,
			"reason", "ciphertext_query_forbidden",
			"decision", "reject",
			"basis", "random-nonce ciphertext is non-deterministic; must decrypt before comparing",
		)
		return nil, ErrCiphertextOperation
	}
	all, err := s.snapshotPlaintext(ctx)
	if err != nil {
		return nil, err
	}
	matched := make([]Object, 0)
	for _, obj := range all {
		if bytes.Equal(obj.Attributes[p.Attribute], p.Equals) {
			matched = append(matched, obj)
		}
	}
	s.log(ctx, slog.LevelInfo, "query evaluated",
		"attribute", p.Attribute,
		"want", summarize(p.Equals),
		"matched", len(matched),
		"decision", "decrypt_then_compare",
		"basis", "all candidate objects were decrypted before equality comparison",
	)
	return matched, nil
}

// SortAll 解密后按指定属性排序，返回全部对象的明文副本。
func (s *Store) SortAll(ctx context.Context, order SortOrder) ([]Object, error) {
	if _, secret := s.sensitive[order.Attribute]; secret && order.OnCiphertext {
		s.log(ctx, slog.LevelWarn, "sort rejected",
			"attribute", order.Attribute,
			"reason", "ciphertext_sort_forbidden",
			"decision", "reject",
			"basis", "ciphertext ordering does not reflect plaintext semantics; must decrypt before sorting",
		)
		return nil, ErrCiphertextOperation
	}
	all, err := s.snapshotPlaintext(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		cmp := bytes.Compare(all[i].Attributes[order.Attribute], all[j].Attributes[order.Attribute])
		if order.Desc {
			return cmp > 0
		}
		return cmp < 0
	})
	s.log(ctx, slog.LevelInfo, "sort evaluated",
		"attribute", order.Attribute,
		"desc", order.Desc,
		"count", len(all),
		"decision", "decrypt_then_sort",
		"basis", "plaintext values were compared after decryption",
	)
	return all, nil
}

// Rewrap 用当前最新密钥重加密全部对象，用于密钥轮换后的数据迁移。
func (s *Store) Rewrap(ctx context.Context) (int, error) {
	ver, err := s.codec.ring.CurrentVersion()
	if err != nil {
		return 0, err
	}
	rewrapped := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, stored := range s.objects {
		for attr, value := range stored.Attributes {
			if _, secret := s.sensitive[attr]; !secret {
				continue
			}
			env, err := EnvelopeOf(value)
			if err != nil {
				return rewrapped, fmt.Errorf("rewrap %s.%s: %w", id, attr, err)
			}
			if env.KeyVer == ver {
				continue
			}
			plain, err := s.codec.Open(value)
			if err != nil {
				return rewrapped, fmt.Errorf("rewrap %s.%s decrypt: %w", id, attr, err)
			}
			sealed, err := s.codec.Seal(plain)
			if err != nil {
				return rewrapped, fmt.Errorf("rewrap %s.%s seal: %w", id, attr, err)
			}
			stored.Attributes[attr] = sealed
			rewrapped++
		}
	}
	s.log(ctx, slog.LevelInfo, "rewrap complete",
		"rewrapped_attributes", rewrapped,
		"target_key_version", ver,
		"decision", "decrypt_with_old_version_then_reseal",
		"basis", "old key versions remain in the key ring",
	)
	return rewrapped, nil
}

// snapshotPlaintext 在读锁下拷贝全部对象并解密敏感属性。
func (s *Store) snapshotPlaintext(ctx context.Context) ([]Object, error) {
	s.mu.RLock()
	storedList := make([]*Object, 0, len(s.objects))
	for _, stored := range s.objects {
		storedList = append(storedList, stored)
	}
	s.mu.RUnlock()

	out := make([]Object, 0, len(storedList))
	for _, stored := range storedList {
		obj := Object{ID: stored.ID, Attributes: make(map[string][]byte, len(stored.Attributes))}
		for attr, value := range stored.Attributes {
			if _, secret := s.sensitive[attr]; !secret {
				obj.Attributes[attr] = append([]byte(nil), value...)
				continue
			}
			plain, err := s.codec.Open(value)
			if err != nil {
				return nil, fmt.Errorf("decrypt %s.%s: %w", stored.ID, attr, err)
			}
			obj.Attributes[attr] = plain
		}
		out = append(out, obj)
	}
	return out, nil
}

func (s *Store) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if s.logger != nil {
		s.logger.Log(ctx, level, msg, args...)
	}
}

func (s *Store) keyVersionOf(value []byte, sensitive bool) any {
	if !sensitive {
		return nil
	}
	if env, err := EnvelopeOf(value); err == nil {
		return uint32(env.KeyVer)
	}
	return nil
}

func (s *Store) nonceOf(value []byte, sensitive bool) any {
	if !sensitive {
		return nil
	}
	if env, err := EnvelopeOf(value); err == nil {
		return fmt.Sprintf("%x", env.Nonce)
	}
	return nil
}

func (s *Store) ciphertextSummary(stored *Object) map[string]string {
	out := make(map[string]string)
	for attr, value := range stored.Attributes {
		if _, secret := s.sensitive[attr]; secret {
			out[attr] = summarize(value)
		}
	}
	return out
}

// summarize 输出密文的短摘要，日志中不打印明文。
func summarize(b []byte) string {
	const limit = 24
	if len(b) <= limit {
		return fmt.Sprintf("%x", b)
	}
	return fmt.Sprintf("%x...(len=%d)", b[:limit], len(b))
}
