package handler

import (
	"ontology/dedupe"
	"ontology/history"
	"sync"
)

// pending 是已接受但尚未应用的更新。
type pending struct {
	uid   []byte
	delta int64
	seq   int64 // 对应 U 事件序号
}

// inst 是单个工作流实例的全部内存状态。
type inst struct {
	mu     sync.Mutex
	log    *history.Log
	dedup  *dedupe.Table
	cap    int64
	s      int64 // 已应用值
	p      int64 // 投影值：s + 队列中所有 delta 之和
	queue  []pending
	closed bool
}

// Handler 管理多个实例；不同实例可并发，同一实例操作串行化。
type Handler struct {
	mu    sync.RWMutex
	store *history.Store
	k     int
	m     map[string]*inst
}

// New 创建处理器，k 为去重表容量（1..10^6）。
func New(k int) *Handler {
	if k < MinDedupeK || k > MaxDedupeK {
		return nil
	}
	return &Handler{store: history.NewStore(), k: k, m: make(map[string]*inst)}
}

// Create 建立实例，cap 为取值上界（0..10^12），已应用值初值 0。
func (h *Handler) Create(instance []byte, cap int64) error {
	if len(instance) == 0 || cap < 0 || cap > MaxCap {
		return ErrInvalidParam
	}
	if !h.store.Create(instance) {
		return ErrExists
	}
	st := &inst{
		log:   h.store.Get(instance),
		dedup: dedupe.New(h.k),
		cap:   cap,
	}
	h.mu.Lock()
	h.m[string(instance)] = st
	h.mu.Unlock()
	return nil
}

// Update 提交带唯一编号的更新，返回登记结果（Rejected/Accepted/Completed/Aborted）。
func (h *Handler) Update(instance, uid []byte, delta int64) (Result, error) {
	if len(instance) == 0 || len(uid) == 0 || delta < MinDelta || delta > MaxDelta {
		return Result{}, ErrInvalidParam
	}
	h.mu.RLock()
	st := h.m[string(instance)]
	h.mu.RUnlock()
	if st == nil {
		return Result{}, ErrNotFound
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if r, ok := st.dedup.Lookup(uid); ok { // 去重命中先于关闭检查，原样返回
		return r.(Result), nil
	}
	if st.closed {
		return Result{}, ErrClosed
	}
	if st.p+delta < 0 || st.p+delta > st.cap {
		r := Result{Kind: Rejected}
		st.dedup.Put(uid, r) // Rejected 登记去重，但不写历史、不耗序号
		return r, nil
	}
	e := st.log.AppendUpdate(uid, delta)
	st.p += delta
	st.queue = append(st.queue, pending{
		uid:   append([]byte(nil), uid...),
		delta: delta,
		seq:   e.Index,
	})
	r := Result{Kind: Accepted, Seq: e.Index}
	st.dedup.Put(uid, r)
	return r, nil
}
