// Package fkjoin 维护左表与右表的外键内连接结果。
//
// 左表每行带一个可空外键：外键非空且右表存在对应行时，结果包含
// (左行ID, 左行值, 右行键, 右行值)。左表的订阅通过先进先出的响应队列
// 异步获知右表状态；投递响应时校验左行哈希，哈希不一致说明左行已经
// 过期变更，该响应被丢弃并计入丢弃数。
package fkjoin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
)

// 可区分的拒绝原因。所有拒绝都在变更生效前判定，因此被拒绝的操作
// 不会改变两表、订阅、队列、结果或丢弃数。
var (
	// ErrNullKey 表示键为空（左行 ID 为空、右行键为 nil/空字节切片，
	// 或左行显式按外键查询时使用了空外键）。
	ErrNullKey = errors.New("fkjoin: null or empty key")
	// ErrEmptyQueue 表示在响应队列为空时要求投递响应。
	ErrEmptyQueue = errors.New("fkjoin: response queue is empty")
	// ErrQueueOverflow 表示一次操作产生的待投递响应会使队列超过
	// 配置的上限。
	ErrQueueOverflow = errors.New("fkjoin: pending response limit exceeded")
)

// Row 是一条内连接结果。
type Row struct {
	LeftID     string
	LeftValue  string
	RightKey   []byte
	RightValue string
}

// response 是队列中的一次右表状态通知。
type response struct {
	leftID string
	key    []byte
	// right 为 nil 表示右表中不存在该键（删除/未找到）。
	right    *string
	leftHash string
	reason   string
}

// Joiner 是两表外键内连接组件。零值不可用，使用 New 创建。
type Joiner struct {
	mu sync.RWMutex

	maxPending int
	logger     Logger

	left    map[string]leftRow
	right   map[string]string
	result  map[string]Row
	queue   []response
	dropped int
}

type leftRow struct {
	value string
	fk    []byte
	hash  string
}

// hashLeftRow 计算左行内容哈希。外键纳入哈希，因此外键或值发生任何
// 变更后，旧订阅产生的在途响应都会因哈希不一致而被丢弃。
func hashLeftRow(id, value string, fk []byte) string {
	h := sha256.New()
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(value))
	h.Write([]byte{0})
	h.Write(fk)
	return hex.EncodeToString(h.Sum(nil))
}

func isEmptyKey(key []byte) bool {
	return len(key) == 0
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// Logger 记录判定依据，便于测试与排查。
type Logger interface {
	Printf(format string, args ...any)
}

// New 创建 Joiner。maxPending 为待投递响应队列上限，必须大于 0；
// logger 为 nil 时不输出日志。
func New(maxPending int, logger Logger) *Joiner {
	if maxPending <= 0 {
		maxPending = 1024
	}
	return &Joiner{
		maxPending: maxPending,
		logger:     logger,
		left:       make(map[string]leftRow),
		right:      make(map[string]string),
		result:     make(map[string]Row),
	}
}

// UpsertLeft 插入或更新左行。fk 为 nil 表示外键为空（不订阅、不在结果中）。
func (j *Joiner) UpsertLeft(id, value string, fk []byte) error {
	if id == "" {
		j.logf("reject UpsertLeft: empty left row id")
		return ErrNullKey
	}
	if fk != nil && isEmptyKey(fk) {
		j.logf("reject UpsertLeft id=%q: empty non-nil foreign key", id)
		return ErrNullKey
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	old, existed := j.left[id]
	newHash := hashLeftRow(id, value, fk)

	// 外键非空时产生一个查询响应；先检查队列上限，超限则整笔拒绝，
	// 两表、订阅、队列与结果均保持不变。
	if fk != nil && len(j.queue)+1 > j.maxPending {
		j.logf("reject UpsertLeft id=%q fk=%q: queue overflow pending=%d limit=%d",
			id, string(fk), len(j.queue), j.maxPending)
		return ErrQueueOverflow
	}

	j.left[id] = leftRow{value: value, fk: cloneBytes(fk), hash: newHash}

	if fk == nil {
		// 外键为空：不订阅，立即撤回任何旧结果。
		delete(j.result, id)
		if existed {
			j.logf("input UpsertLeft id=%q value=%q fk=nil oldfk=%q: no subscription, withdraw result",
				id, value, string(old.fk))
		} else {
			j.logf("input UpsertLeft id=%q value=%q fk=nil: no subscription", id, value)
		}
		return nil
	}

	resp := response{
		leftID:   id,
		key:      cloneBytes(fk),
		leftHash: newHash,
		reason:   "lookup",
	}
	if rv, ok := j.right[string(fk)]; ok {
		v := rv
		resp.right = &v
	}
	if existed {
		j.logf("input UpsertLeft id=%q value=%q fk=%q oldfk=%q oldval=%q: enqueue lookup hit=%v",
			id, value, string(fk), string(old.fk), old.value, resp.right != nil)
	} else {
		j.logf("input UpsertLeft id=%q value=%q fk=%q: enqueue lookup hit=%v",
			id, value, string(fk), resp.right != nil)
	}
	j.queue = append(j.queue, resp)
	return nil
}

// RemoveLeft 删除左行，同时撤回其结果与订阅。
func (j *Joiner) RemoveLeft(id string) error {
	if id == "" {
		j.logf("reject RemoveLeft: empty left row id")
		return ErrNullKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	old, ok := j.left[id]
	if !ok {
		j.logf("input RemoveLeft id=%q: not present, no change", id)
		return nil
	}
	delete(j.left, id)
	delete(j.result, id)
	// 订阅随左行删除：在途响应投递时会因找不到左行而被丢弃。
	j.logf("input RemoveLeft id=%q fk=%q: row, subscription and result withdrawn",
		id, string(old.fk))
	return nil
}

// PutRight 插入或更新右行，并向所有订阅该键的左行产生响应。
func (j *Joiner) PutRight(key []byte, value string) error {
	if isEmptyKey(key) {
		j.logf("reject PutRight: empty right key")
		return ErrNullKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	ks := string(key)
	n := j.subscriberCount(ks)
	// 每个当前订阅该键的左行都会收到一个响应；先校验上限，
	// 超限时右表、订阅、队列、结果全部保持不变。
	if len(j.queue)+n > j.maxPending {
		j.logf("reject PutRight key=%q: queue overflow pending=%d subscribers=%d limit=%d",
			ks, len(j.queue), n, j.maxPending)
		return ErrQueueOverflow
	}

	j.right[ks] = value
	j.fanOut(ks, &value, "put")
	j.logf("input PutRight key=%q value=%q: stored, %d responses enqueued", ks, value, n)
	return nil
}

// RemoveRight 删除右行，并向所有订阅该键的左行产生“不存在”响应。
func (j *Joiner) RemoveRight(key []byte) error {
	if isEmptyKey(key) {
		j.logf("reject RemoveRight: empty right key")
		return ErrNullKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	ks := string(key)
	if _, ok := j.right[ks]; !ok {
		j.logf("input RemoveRight key=%q: not present, no change", ks)
		return nil
	}
	n := j.subscriberCount(ks)
	if len(j.queue)+n > j.maxPending {
		j.logf("reject RemoveRight key=%q: queue overflow pending=%d subscribers=%d limit=%d",
			ks, len(j.queue), n, j.maxPending)
		return ErrQueueOverflow
	}

	delete(j.right, ks)
	j.fanOut(ks, nil, "remove")
	j.logf("input RemoveRight key=%q: removed, %d responses enqueued", ks, n)
	return nil
}

// subscriberCount 调用时须持有 j.mu。
func (j *Joiner) subscriberCount(key string) int {
	n := 0
	for _, lr := range j.left {
		if lr.fk != nil && string(lr.fk) == key {
			n++
		}
	}
	return n
}

// fanOut 按左行 ID 升序向订阅者入队响应，保证同一输入序列下
// 队列内容与输出完全确定。调用时须持有 j.mu。
func (j *Joiner) fanOut(key string, right *string, reason string) {
	ids := make([]string, 0)
	for id, lr := range j.left {
		if lr.fk != nil && string(lr.fk) == key {
			ids = append(ids, id)
		}
	}
	sortStrings(ids)
	for _, id := range ids {
		lr := j.left[id]
		resp := response{
			leftID:   id,
			key:      cloneBytes(lr.fk),
			right:    nil,
			leftHash: lr.hash,
			reason:   reason,
		}
		if right != nil {
			v := *right
			resp.right = &v
		}
		j.queue = append(j.queue, resp)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for k := i; k > 0 && s[k-1] > s[k]; k-- {
			s[k-1], s[k] = s[k], s[k-1]
		}
	}
}

// DeliverOne 投递队首响应并返回其判定结果。
func (j *Joiner) DeliverOne() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if len(j.queue) == 0 {
		j.logf("reject DeliverOne: empty queue")
		return ErrEmptyQueue
	}
	resp := j.queue[0]
	j.queue = j.queue[1:]

	lr, ok := j.left[resp.leftID]
	switch {
	case !ok:
		// 左行已删除：订阅不存在，响应过期。
		j.dropped++
		j.logf("output discard id=%q key=%q reason=%q cause=left-row-gone dropped=%d",
			resp.leftID, string(resp.key), resp.reason, j.dropped)
	case lr.hash != resp.leftHash:
		// 哈希不一致：左行在响应产生后又被修改，响应过期。
		j.dropped++
		j.logf("output discard id=%q key=%q reason=%q cause=hash-mismatch dropped=%d",
			resp.leftID, string(resp.key), resp.reason, j.dropped)
	case resp.right == nil:
		delete(j.result, resp.leftID)
		j.logf("output withdraw id=%q key=%q reason=%q: right absent",
			resp.leftID, string(resp.key), resp.reason)
	default:
		j.result[resp.leftID] = Row{
			LeftID:     resp.leftID,
			LeftValue:  lr.value,
			RightKey:   cloneBytes(resp.key),
			RightValue: *resp.right,
		}
		j.logf("output join id=%q value=%q key=%q right=%q reason=%q",
			resp.leftID, lr.value, string(resp.key), *resp.right, resp.reason)
	}
	return nil
}

// Drain 投递全部待处理响应。
func (j *Joiner) Drain() error {
	j.mu.Lock()
	empty := len(j.queue) == 0
	j.mu.Unlock()
	if empty {
		j.logf("reject Drain: empty queue")
		return ErrEmptyQueue
	}
	for {
		j.mu.RLock()
		n := len(j.queue)
		j.mu.RUnlock()
		if n == 0 {
			return nil
		}
		if err := j.DeliverOne(); err != nil {
			return err
		}
	}
}

// Snapshot 返回当前结果的逐条一致快照。
func (j *Joiner) Snapshot() []Row {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.snapshotLocked()
}

// Pending 返回待投递响应数量。
func (j *Joiner) Pending() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.queue)
}

// Dropped 返回因哈希不一致被丢弃的响应数。
func (j *Joiner) Dropped() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.dropped
}

// NaiveJoin 直接依据两表当前状态计算朴素内连接，供校验与测试使用。
func (j *Joiner) NaiveJoin() []Row {
	j.mu.RLock()
	defer j.mu.RUnlock()

	naive := make(map[string]Row)
	for id, lr := range j.left {
		if lr.fk == nil {
			continue
		}
		if rv, ok := j.right[string(lr.fk)]; ok {
			naive[id] = Row{
				LeftID:     id,
				LeftValue:  lr.value,
				RightKey:   cloneBytes(lr.fk),
				RightValue: rv,
			}
		}
	}
	return rowsToSortedSlice(naive)
}

// snapshotLocked 调用时至少持有读锁。
func (j *Joiner) snapshotLocked() []Row {
	return rowsToSortedSlice(j.result)
}

func rowsToSortedSlice(m map[string]Row) []Row {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortStrings(ids)
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		r := m[id]
		r.RightKey = cloneBytes(r.RightKey)
		out = append(out, r)
	}
	return out
}
