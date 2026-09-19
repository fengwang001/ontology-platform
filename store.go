package ontology

import (
	"crypto/rand"
	"sort"
	"sync"
)

// Store 是按字符串主键排序的对象集合，状态只保存在进程内存。
// 遍历会话与写入并发进行：写路径只通过原子操作通知会话，不会被 Scan 阻塞。
type Store struct {
	mu       sync.RWMutex
	items    map[string]int
	secret   []byte
	sessions map[[cursorIDLen]byte]*session
}

// NewStore 创建一个空集合，密钥为进程内随机值（游标随进程失效）。
func NewStore() *Store {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return &Store{
		items:    make(map[string]int),
		secret:   secret,
		sessions: make(map[[cursorIDLen]byte]*session),
	}
}

// Put 插入新主键或更新已有主键，返回该主键是否为本次新增。
func (st *Store) Put(key string, value int) (inserted bool) {
	st.mu.Lock()
	if _, ok := st.items[key]; ok {
		st.items[key] = value
		st.mu.Unlock()
		return false
	}
	st.items[key] = value
	for _, sess := range st.sessions {
		// 快照语义：新键永远不会被本次已开始的遍历访问，均计为插入跳过。
		sess.inserts.Add(1)
		sess.insertsAhead.Add(1)
	}
	st.mu.Unlock()
	return true
}

// Delete 删除主键，返回它是否原本存在。对遍历会话仅在被删元素属于
// 首次 Scan 快照、且尚未翻到时才计为“丢弃”。
func (st *Store) Delete(key string) (deleted bool) {
	st.mu.Lock()
	if _, ok := st.items[key]; !ok {
		st.mu.Unlock()
		return false
	}
	delete(st.items, key)
	for _, sess := range st.sessions {
		idx := sort.SearchStrings(sess.snapshot, key)
		if idx >= len(sess.snapshot) || sess.snapshot[idx] != key {
			continue // 该元素在会话创建时不存在，与本次遍历无关。
		}
		sess.mu.Lock()
		ahead := int64(idx) >= sess.position
		sess.mu.Unlock()
		sess.deletes.Add(1)
		if ahead {
			sess.deletesAhead.Add(1)
		}
	}
	st.mu.Unlock()
	return true
}

// Len 返回当前元素数。
func (st *Store) Len() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.items)
}

func (st *Store) snapshotKeys() []string {
	st.mu.RLock()
	keys := make([]string, 0, len(st.items))
	for key := range st.items {
		keys = append(keys, key)
	}
	st.mu.RUnlock()
	sort.Strings(keys)
	return keys
}

func (st *Store) registerSession(s *session) {
	var kid [cursorIDLen]byte
	copy(kid[:], s.id)
	st.mu.Lock()
	st.sessions[kid] = s
	st.mu.Unlock()
}

func (st *Store) lookupSession(id []byte) *session {
	var kid [cursorIDLen]byte
	copy(kid[:], id)
	st.mu.RLock()
	s := st.sessions[kid]
	st.mu.RUnlock()
	return s
}
