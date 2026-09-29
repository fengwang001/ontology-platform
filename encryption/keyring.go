package encryption

import (
	"crypto/rand"
	"errors"
	"sort"
	"sync"
)

// KeyVersion 标识一个密钥版本。
type KeyVersion uint32

// keyEntry 保存某一版本的原始 AES-256 密钥。
// 密钥只存在于 KeyRing 中，绝不与数据混存。
type keyEntry struct {
	version KeyVersion
	key     []byte
}

// KeyRing 保存当前加密密钥以及全部历史版本密钥。
// 旧版本密钥必须保留，以便密钥轮换后仍可解密旧数据。
type KeyRing struct {
	mu      sync.RWMutex
	keys    map[KeyVersion][]byte
	current KeyVersion
}

// NewKeyRing 创建空密钥环。
func NewKeyRing() *KeyRing {
	return &KeyRing{keys: make(map[KeyVersion][]byte)}
}

// GenerateKey 生成一把新的 AES-256 随机密钥并加入密钥环。
func (r *KeyRing) GenerateKey() (KeyVersion, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.current + 1
	if _, exists := r.keys[next]; exists {
		return 0, errors.New("encryption: internal error, generated key version already exists")
	}
	r.keys[next] = key
	r.current = next
	return next, nil
}

// Rotate 生成新版本密钥并设为当前版本；旧版本全部保留。
func (r *KeyRing) Rotate() (KeyVersion, error) {
	// GenerateKey 只新增不删除：轮换天然保留全部旧版本，
	// 因此旧数据始终可解。
	return r.GenerateKey()
}

// RetireKey 尝试删除旧版本密钥。只要仍有任一旧版本存在
// （即删除会令旧密文不可解），该操作一律被拒绝。
func (r *KeyRing) RetireKey(version KeyVersion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[version]; !exists {
		return ErrKeyNotFound
	}
	// 退役任何已存在的密钥版本都可能使该版本加密的历史数据
	// 永久不可解，因此一律拒绝且不修改密钥环。
	return ErrKeyRotationBreaksOldData
}

// AddExternalKey 注入由外部 KMS 托管的密钥（密钥与数据分离）。
func (r *KeyRing) AddExternalKey(version KeyVersion, key []byte, makeCurrent bool) error {
	if len(key) != 32 {
		return errors.New("encryption: external key must be 32 bytes (AES-256)")
	}
	if version == 0 {
		return errors.New("encryption: key version must be greater than zero")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[version]; exists {
		return errors.New("encryption: key version already exists")
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	r.keys[version] = keyCopy
	if makeCurrent || r.current == 0 {
		r.current = version
	}
	return nil
}

// CurrentVersion 返回当前加密密钥版本。
func (r *KeyRing) CurrentVersion() (KeyVersion, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.current == 0 {
		return 0, errors.New("encryption: key ring is empty")
	}
	return r.current, nil
}

// get 取出指定版本密钥副本。
func (r *KeyRing) get(version KeyVersion) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.keys[version]
	if !ok {
		return nil, ErrKeyNotFound
	}
	out := make([]byte, len(key))
	copy(out, key)
	return out, nil
}

// Versions 返回所有密钥版本（升序）。
func (r *KeyRing) Versions() []KeyVersion {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]KeyVersion, 0, len(r.keys))
	for v := range r.keys {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
