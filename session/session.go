// Package session 实现按偏移协商的多租户断点续传上传会话服务。
// 所有操作经单互斥锁串行化，并发调用等价于某个串行顺序。
package session

import (
	"bytes"
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"

	"ontology/journal"
	"ontology/quota"
)

// 参数上界。
const (
	MaxNow   = int64(1_000_000_000_000) // 1e12 秒
	MaxTotal = int64(1_000_000_000_000) // 1e12 字节
	MaxChunk = 1 << 20                  // 单块最大 1MiB
)

// Status 描述会话生命周期。
type Status string

const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusAborted   Status = "aborted"
	StatusExpired   Status = "expired"
)

var (
	// ErrInvalidParam 表示参数非法（空租户/键、越界的 total/off/now/sid、超长数据）。
	ErrInvalidParam = errors.New("session: 参数非法")
	// ErrClockRewind 表示 now 小于上一次被接受操作的 now。
	ErrClockRewind = errors.New("session: 时钟回退")
	// ErrTooManySessions 表示该租户开启中的会话数已达上限 M。
	ErrTooManySessions = errors.New("session: 会话数超限")
	// ErrNoSuchSession 表示会话号从未发出。
	ErrNoSuchSession = errors.New("session: 会话从未发出")
	// ErrSessionClosed 表示会话已完成或已中止。
	ErrSessionClosed = errors.New("session: 会话已完成或已中止")
	// ErrSessionExpired 表示会话已到期。
	ErrSessionExpired = errors.New("session: 会话已到期")
	// ErrGap 表示写入偏移越过已提交偏移，出现缺口。
	ErrGap = errors.New("session: 缺口")
	// ErrOverflow 表示 off+len(data) 越过 total。
	ErrOverflow = errors.New("session: 越界")
	// ErrConflict 表示数据与已提交字节不一致。
	ErrConflict = errors.New("session: 冲突")
	// ErrIncomplete 表示 Complete 时已提交偏移未达 total。
	ErrIncomplete = errors.New("session: 未完成")
	// ErrJournal 表示日志追加失败，操作整体拒绝。
	ErrJournal = journal.ErrSink
)

// 日志记录操作名。
const (
	opSetQuota = "setquota"
	opCreate   = "create"
	opChunk    = "chunk"
	opComplete = "complete"
	opAbort    = "abort"
)

// opRecord 是一条日志记录，含复现操作所需的全部入参。
type opRecord struct {
	Op     string `json:"op"`
	Tenant string `json:"tenant,omitempty"`
	Quota  int64  `json:"quota,omitempty"`
	Key    string `json:"key,omitempty"`
	Total  int64  `json:"total,omitempty"`
	Sid    int64  `json:"sid,omitempty"`
	Off    int64  `json:"off,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Now    int64  `json:"now,omitempty"`
}

type session struct {
	sid       int64
	tenant    string
	key       string
	total     int64
	c         int64
	expiry    int64
	status    Status
	buf       []byte
	heapIndex int
}

// Service 是上传会话服务。
type Service struct {
	mu       sync.Mutex
	led      *quota.Ledger
	jr       *journal.Journal
	m        int64
	ttl      int64
	maxNow   int64
	nextSid  int64
	sessions map[int64]*session
	open     sessionHeap
	popped   int
}

// New 构造服务：m 为每租户最大并发开启会话数（1..1000），
// ttl 为会话有效期秒数（1..1e9）；sink 为 nil 时用内存 Sink。
func New(m, ttl int64, sink journal.Sink) *Service {
	if m < 1 || m > 1000 {
		panic("session: M 须在 1..1000")
	}
	if ttl < 1 || ttl > 1_000_000_000 {
		panic("session: T 须在 1..1e9")
	}
	if sink == nil {
		sink = &journal.MemSink{}
	}
	return &Service{
		led:      quota.NewLedger(),
		jr:       journal.New(sink),
		m:        m,
		ttl:      ttl,
		maxNow:   -1,
		nextSid:  1,
		sessions: map[int64]*session{},
	}
}

// probeExpired 试探性弹出 now 时刻已到期（expiry <= now）的开启中会话。
// 返回弹出列表与弹出次数（含一次未过期的探针弹出）。
// 调用方须在操作被拒时用 restore 放回，或在被接受时用 land 落地。
func (s *Service) probeExpired(now int64) (expired []*session, pops int) {
	for len(s.open) > 0 {
		top := heap.Pop(&s.open).(*session)
		pops++
		if top.expiry > now {
			heap.Push(&s.open, top)
			break
		}
		expired = append(expired, top)
	}
	return expired, pops
}

// restore 把试探弹出的会话放回堆（操作被拒时调用）。
func (s *Service) restore(expired []*session) {
	for _, sess := range expired {
		heap.Push(&s.open, sess)
	}
}

// land 落地回收：释放预留、状态置为已到期、累计 popped、推进时钟。
func (s *Service) land(expired []*session, pops int, now int64) {
	for _, sess := range expired {
		sess.status = StatusExpired
		sess.buf = nil
		s.led.Release(sess.tenant, sess.total)
	}
	s.popped += pops
	s.maxNow = now
}

func marshalRecord(rec opRecord) []byte {
	raw, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return raw
}

// SetQuota 设置租户额度；q 不得小于该租户 U+R。
func (s *Service) SetQuota(tenant string, q int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.led.CheckSetQuota(tenant, q); err != nil {
		return err
	}
	if err := s.jr.Append(marshalRecord(opRecord{Op: opSetQuota, Tenant: tenant, Quota: q})); err != nil {
		return err
	}
	s.led.ApplySetQuota(tenant, q)
	return nil
}

// Create 开启上传会话，全量预留 total，返回严格递增的会话号。
// 拒绝次序：参数非法 > 时钟回退 > 会话数超限 > 额度不足 > 日志失败。
func (s *Service) Create(tenant, key string, total, now int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenant == "" || key == "" || total < 1 || total > MaxTotal || now < 0 || now > MaxNow {
		return 0, ErrInvalidParam
	}
	if now < s.maxNow {
		return 0, ErrClockRewind
	}
	expired, pops := s.probeExpired(now)
	reject := func(err error) (int64, error) {
		s.restore(expired)
		return 0, err
	}
	view := s.led.View(tenant)
	var relTotal int64
	var relOpen int
	for _, sess := range expired {
		if sess.tenant == tenant {
			relTotal += sess.total
			relOpen++
		}
	}
	if int64(view.Open-relOpen) >= s.m {
		return reject(ErrTooManySessions)
	}
	if view.Used+view.Reserved-relTotal+total > view.Quota {
		return reject(quota.ErrQuotaExceeded)
	}
	rec := opRecord{Op: opCreate, Tenant: tenant, Key: key, Total: total, Now: now}
	if err := s.jr.Append(marshalRecord(rec)); err != nil {
		return reject(err)
	}
	s.land(expired, pops, now)
	sid := s.nextSid
	s.nextSid++
	sess := &session{
		sid:    sid,
		tenant: tenant,
		key:    key,
		total:  total,
		expiry: now + s.ttl,
		status: StatusOpen,
	}
	s.sessions[sid] = sess
	heap.Push(&s.open, sess)
	s.led.Reserve(tenant, total)
	return sid, nil
}

// Chunk 按偏移写入数据（幂等续传）；纯重放不刷新到期时刻。
// 拒绝次序：参数非法 > 时钟回退 > 会话类 > 缺口 > 越界 > 冲突 > 日志失败。
func (s *Service) Chunk(sid, off int64, data []byte, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || off < 0 || off > MaxTotal || len(data) > MaxChunk || now < 0 || now > MaxNow {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRewind
	}
	expired, pops := s.probeExpired(now)
	reject := func(err error) error {
		s.restore(expired)
		return err
	}
	sess, err := s.lookup(sid, now)
	if err != nil {
		return reject(err)
	}
	if off > sess.c {
		return reject(ErrGap)
	}
	end := off + int64(len(data))
	if end > sess.total {
		return reject(ErrOverflow)
	}
	cmpLen := sess.c - off
	if int64(len(data)) < cmpLen {
		cmpLen = int64(len(data))
	}
	if cmpLen > 0 && !bytes.Equal(data[:cmpLen], sess.buf[off:off+cmpLen]) {
		return reject(ErrConflict)
	}
	rec := opRecord{Op: opChunk, Sid: sid, Off: off, Data: data, Now: now}
	if err := s.jr.Append(marshalRecord(rec)); err != nil {
		return reject(err)
	}
	s.land(expired, pops, now)
	if end > sess.c {
		sess.buf = append(sess.buf, data[cmpLen:]...)
		sess.c = end
		sess.expiry = now + s.ttl
		heap.Fix(&s.open, sess.heapIndex)
	}
	return nil
}

// lookup 会话类判定：从未发出 > 已完成或已中止 > 已到期。
func (s *Service) lookup(sid, now int64) (*session, error) {
	sess, ok := s.sessions[sid]
	if !ok {
		return nil, ErrNoSuchSession
	}
	if sess.status == StatusCompleted || sess.status == StatusAborted {
		return nil, ErrSessionClosed
	}
	if sess.status == StatusExpired || sess.expiry <= now {
		return nil, ErrSessionExpired
	}
	return sess, nil
}

// Complete 要求 c==total，预留转用量并按同键旧对象冲正 U。
func (s *Service) Complete(sid, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || now < 0 || now > MaxNow {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRewind
	}
	expired, pops := s.probeExpired(now)
	reject := func(err error) error {
		s.restore(expired)
		return err
	}
	sess, err := s.lookup(sid, now)
	if err != nil {
		return reject(err)
	}
	if sess.c != sess.total {
		return reject(fmt.Errorf("%w: 剩余 %d", ErrIncomplete, sess.total-sess.c))
	}
	if err := s.jr.Append(marshalRecord(opRecord{Op: opComplete, Sid: sid, Now: now})); err != nil {
		return reject(err)
	}
	s.land(expired, pops, now)
	heap.Remove(&s.open, sess.heapIndex)
	sess.status = StatusCompleted
	sess.buf = nil
	s.led.Commit(sess.tenant, sess.key, sess.total)
	return nil
}

// Abort 中止会话并释放其预留。
func (s *Service) Abort(sid, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || now < 0 || now > MaxNow {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRewind
	}
	expired, pops := s.probeExpired(now)
	reject := func(err error) error {
		s.restore(expired)
		return err
	}
	sess, err := s.lookup(sid, now)
	if err != nil {
		return reject(err)
	}
	if err := s.jr.Append(marshalRecord(opRecord{Op: opAbort, Sid: sid, Now: now})); err != nil {
		return reject(err)
	}
	s.land(expired, pops, now)
	heap.Remove(&s.open, sess.heapIndex)
	sess.status = StatusAborted
	sess.buf = nil
	s.led.Release(sess.tenant, sess.total)
	return nil
}

// Query 只读查询：返回已提交偏移与到期时刻；不推进时钟、不落地回收。
func (s *Service) Query(sid, now int64) (c, expiry int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || now < 0 || now > MaxNow {
		return 0, 0, ErrInvalidParam
	}
	if now < s.maxNow {
		return 0, 0, ErrClockRewind
	}
	sess, err := s.lookup(sid, now)
	if err != nil {
		return 0, 0, err
	}
	return sess.c, sess.expiry, nil
}

// Records 返回已接受操作的日志记录序列。
func (s *Service) Records() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jr.Records()
}

// Replay 用记录序列重放出一个等价服务（构造参数 m、ttl 须一致）。
func Replay(records [][]byte, m, ttl int64) (*Service, error) {
	svc := New(m, ttl, nil)
	for i, raw := range records {
		var rec opRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("replay #%d: %w", i, err)
		}
		var err error
		switch rec.Op {
		case opSetQuota:
			err = svc.SetQuota(rec.Tenant, rec.Quota)
		case opCreate:
			_, err = svc.Create(rec.Tenant, rec.Key, rec.Total, rec.Now)
		case opChunk:
			err = svc.Chunk(rec.Sid, rec.Off, rec.Data, rec.Now)
		case opComplete:
			err = svc.Complete(rec.Sid, rec.Now)
		case opAbort:
			err = svc.Abort(rec.Sid, rec.Now)
		default:
			err = fmt.Errorf("未知操作 %q", rec.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("replay #%d (%s): %w", i, rec.Op, err)
		}
	}
	return svc, nil
}

// TenantState 是租户账本快照。
type TenantState struct {
	Quota    int64
	Used     int64
	Reserved int64
	Open     int
	Objects  map[string]int64
}

// SessionState 是会话快照。
type SessionState struct {
	Tenant   string
	Key      string
	Total    int64
	C        int64
	Expiry   int64
	Status   Status
	DataHash uint64
}

// Snapshot 是服务全量状态快照，可逐字段比较。
type Snapshot struct {
	Now      int64
	NextSid  int64
	Tenants  map[string]TenantState
	Sessions map[int64]SessionState
}

// Snapshot 返回当前状态快照。
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Now:      s.maxNow,
		NextSid:  s.nextSid,
		Tenants:  map[string]TenantState{},
		Sessions: map[int64]SessionState{},
	}
	for _, name := range s.led.Tenants() {
		view := s.led.View(name)
		snap.Tenants[name] = TenantState{
			Quota:    view.Quota,
			Used:     view.Used,
			Reserved: view.Reserved,
			Open:     view.Open,
			Objects:  view.Objects,
		}
	}
	for sid, sess := range s.sessions {
		snap.Sessions[sid] = SessionState{
			Tenant:   sess.tenant,
			Key:      sess.key,
			Total:    sess.total,
			C:        sess.c,
			Expiry:   sess.expiry,
			Status:   sess.status,
			DataHash: hashBytes(sess.buf),
		}
	}
	return snap
}

func hashBytes(data []byte) uint64 {
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}

// sortedSids 返回快照中排序后的会话号，供测试打印。
func (snap Snapshot) sortedSids() []int64 {
	sids := make([]int64, 0, len(snap.Sessions))
	for sid := range snap.Sessions {
		sids = append(sids, sid)
	}
	sort.Slice(sids, func(i, j int) bool { return sids[i] < sids[j] })
	return sids
}
