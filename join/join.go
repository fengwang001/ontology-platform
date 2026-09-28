// Package join 实现两表外键内连接组件。
//
// 左表每行携带可空外键，右表按键存储。左右两侧通过订阅与异步的
// 先进先出响应队列通信：任何使某行连接结果可能变化的变更都会
// 产生一条响应并入队，响应按产生顺序投递。投递时校验左行哈希，
// 不一致说明响应已过期，直接丢弃并计数。
package join

import (
	"errors"
	"hash/fnv"
	"sort"
	"sync"
)

// 可区分的拒绝原因，均被拒绝的操作不会改变两表、订阅、队列、结果或丢弃数。
var (
	// ErrEmptyKey 表示左行 ID 或右表键为空。
	ErrEmptyKey = errors.New("join: 键为空，拒绝操作")
	// ErrEmptyQueue 表示在空队列上执行投递。
	ErrEmptyQueue = errors.New("join: 响应队列为空，拒绝投递")
	// ErrTooManyPending 表示待投递响应将超过上限。
	ErrTooManyPending = errors.New("join: 待投递响应超限，拒绝操作")
)

// LeftRow 是左表的一行，FK 可空。
type LeftRow struct {
	ID    string
	FK    *string
	Value string
	Hash  uint64
}

// ResultEntry 是内连接结果中的一条。
type ResultEntry struct {
	LeftID     string
	LeftValue  string
	Key        string
	RightValue string
}

// response 是一条待投递的订阅响应，携带产生时刻的左右两侧快照。
type response struct {
	leftID     string
	leftHash   uint64
	leftValue  string
	fk         *string
	rightValue *string
}

// Join 维护两表外键内连接的结果，所有方法均可并发调用。
type Join struct {
	mu         sync.RWMutex
	left       map[string]LeftRow
	right      map[string]string
	queue      []response
	result     map[string]ResultEntry
	dropped    int
	maxPending int
}

// New 创建组件，maxPending 为待投递响应上限（必须为正）。
func New(maxPending int) *Join {
	if maxPending <= 0 {
		maxPending = 1
	}
	return &Join{
		left:       make(map[string]LeftRow),
		right:      make(map[string]string),
		result:     make(map[string]ResultEntry),
		maxPending: maxPending,
	}
}

func hashRow(id, value string, fk *string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(value))
	h.Write([]byte{0})
	if fk == nil {
		h.Write([]byte{0})
	} else {
		h.Write([]byte{1})
		h.Write([]byte(*fk))
	}
	return h.Sum64()
}

func strPtr(s string) *string { return &s }

// UpsertLeft 插入或更新左行，并为其产生一条订阅响应。
func (j *Join) UpsertLeft(id, value string, fk *string) error {
	if id == "" {
		return ErrEmptyKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.queue) >= j.maxPending {
		return ErrTooManyPending
	}
	row := LeftRow{ID: id, Value: value, Hash: hashRow(id, value, fk)}
	if fk != nil {
		row.FK = strPtr(*fk)
	}
	j.left[id] = row
	j.enqueueLocked(row)
	return nil
}

// DeleteLeft 删除左行并立即撤回其结果；队列中该行的过期响应
// 将在投递时因哈希校验失败被丢弃。
func (j *Join) DeleteLeft(id string) error {
	if id == "" {
		return ErrEmptyKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.left, id)
	delete(j.result, id)
	return nil
}

// UpsertRight 插入或更新右表行，并为每个订阅该键的左行产生响应。
func (j *Join) UpsertRight(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	affected := j.subscribersLocked(key)
	if len(j.queue)+len(affected) > j.maxPending {
		return ErrTooManyPending
	}
	j.right[key] = value
	for _, row := range affected {
		j.enqueueLocked(row)
	}
	return nil
}

// DeleteRight 删除右表键，并为每个订阅该键的左行产生撤回响应。
func (j *Join) DeleteRight(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	affected := j.subscribersLocked(key)
	if len(j.queue)+len(affected) > j.maxPending {
		return ErrTooManyPending
	}
	delete(j.right, key)
	for _, row := range affected {
		j.enqueueLocked(row)
	}
	return nil
}

// subscribersLocked 按左行 ID 升序返回订阅某键的左行，保证入队顺序确定。
func (j *Join) subscribersLocked(key string) []LeftRow {
	var rows []LeftRow
	for _, row := range j.left {
		if row.FK != nil && *row.FK == key {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].ID < rows[b].ID })
	return rows
}

// enqueueLocked 以产生时刻的右表快照生成响应并入队。
func (j *Join) enqueueLocked(row LeftRow) {
	resp := response{
		leftID:    row.ID,
		leftHash:  row.Hash,
		leftValue: row.Value,
	}
	if row.FK != nil {
		resp.fk = strPtr(*row.FK)
		if v, ok := j.right[*row.FK]; ok {
			resp.rightValue = strPtr(v)
		}
	}
	j.queue = append(j.queue, resp)
}

// DeliverOne 投递队首响应。哈希与当前左行不一致时丢弃并计数。
func (j *Join) DeliverOne() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.queue) == 0 {
		return ErrEmptyQueue
	}
	resp := j.queue[0]
	j.queue = j.queue[1:]
	row, ok := j.left[resp.leftID]
	if !ok || row.Hash != resp.leftHash {
		j.dropped++
		return nil
	}
	if resp.fk != nil && resp.rightValue != nil {
		j.result[resp.leftID] = ResultEntry{
			LeftID:     resp.leftID,
			LeftValue:  resp.leftValue,
			Key:        *resp.fk,
			RightValue: *resp.rightValue,
		}
	} else {
		delete(j.result, resp.leftID)
	}
	return nil
}

// DeliverAll 排空全部待投递响应。
func (j *Join) DeliverAll() {
	for {
		if err := j.DeliverOne(); err != nil {
			return
		}
	}
}

// Snapshot 返回按左行 ID 排序的结果视图，可并发读取且逐条一致。
func (j *Join) Snapshot() []ResultEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]ResultEntry, 0, len(j.result))
	for _, e := range j.result {
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].LeftID < out[b].LeftID })
	return out
}

// Dropped 返回因哈希校验失败被丢弃的响应数。
func (j *Join) Dropped() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.dropped
}

// Pending 返回待投递响应数。
func (j *Join) Pending() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.queue)
}
