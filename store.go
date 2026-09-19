package ontology

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Store 是按字符串主键排序的内存对象集合，并发安全。
// 遍历会话只持有快照键列表，不阻塞写入。
type Store struct {
	mu       sync.RWMutex
	data     map[string]int
	sessions map[string]*session

	codecOnce sync.Once
	gcm       cipher.AEAD
	codecErr  error
}

// NewStore 创建空集合。
func NewStore() *Store {
	return &Store{data: make(map[string]int), sessions: make(map[string]*session)}
}

// Put 插入或更新一个对象；插入新主键会触发遍历的插入变更标记。
func (s *Store) Put(key string, value int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		for _, sn := range s.sessions {
			sn.mu.Lock()
			sn.inserts++
			sn.mu.Unlock()
		}
	}
	s.data[key] = value
}

// Delete 删除一个主键；删除成功会触发遍历的删除变更标记。
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; !ok {
		return
	}
	delete(s.data, key)
	for _, sn := range s.sessions {
		sn.mu.Lock()
		sn.deletes++
		sn.mu.Unlock()
	}
}

// Len 返回当前元素数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// snapshot 返回当前排序键与取值的副本，调用方不得依赖其长期有效性。
func (s *Store) snapshot() ([]string, map[string]int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make(map[string]int, len(s.data))
	for k, v := range s.data {
		vals[k] = v
	}
	return keys, vals
}

// liveValues 返回当前存活主键到值的映射（遍历期间的删除/更新可见）。
func (s *Store) liveValues() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	live := make(map[string]int, len(s.data))
	for k, v := range s.data {
		live[k] = v
	}
	return live
}

func (s *Store) initCodec() {
	s.codecOnce.Do(func() {
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			s.codecErr = err
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			s.codecErr = err
			return
		}
		s.gcm, s.codecErr = cipher.NewGCM(block)
	})
}

// encodeCursor 生成不透明游标：明文主键经 AES-GCM 加密后 base64 编码，
// 不暴露主键；篡改/截断会在 decode 时被 GCM 校验拒绝。
func (s *Store) encodeCursor(sessionID string, frontier int) (string, error) {
	s.initCodec()
	if s.codecErr != nil {
		return "", s.codecErr
	}
	sid := []byte(sessionID)
	plain := make([]byte, len(sid)+8)
	copy(plain, sid)
	binary.BigEndian.PutUint64(plain[len(sid):], uint64(frontier))
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := s.gcm.Seal(nonce, nonce, plain, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// decodeCursor 校验游标完整性。格式/校验失败返回 ErrInvalidCursor。
func (s *Store) decodeCursor(tok string) (string, int, error) {
	s.initCodec()
	if s.codecErr != nil {
		return "", 0, s.codecErr
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	ns := s.gcm.NonceSize()
	if len(raw) < ns {
		return "", 0, fmt.Errorf("%w: truncated", ErrInvalidCursor)
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	plain, err := s.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	if len(plain) < 8 {
		return "", 0, fmt.Errorf("%w: malformed payload", ErrInvalidCursor)
	}
	sid := string(plain[:len(plain)-8])
	frontier := int(binary.BigEndian.Uint64(plain[len(plain)-8:]))
	return sid, frontier, nil
}
