package session

import (
	"container/heap"
	"errors"
	"ontology/journal"
	"ontology/quota"
	"sync"
)

// State 是会话生命周期状态。
type State int

const (
	StateOpen State = iota
	StateComplete
	StateAborted
	StateExpired
)

// Info 是单个会话的只读视图。
type Info struct {
	SID       int64
	Tenant    string
	Key       string
	Total     int64
	Committed int64
	Expiry    int64
	State     State
}

type session struct {
	id      int64
	tenant  string
	key     string
	total   int64
	c       int64
	expiry  int64
	state   State
	data    []byte
	heapIdx int
}

// Service 是多租户续传上传服务。
type Service struct {
	mu      sync.Mutex
	ledger  *quota.Ledger
	sink    journal.Sink
	t       int64
	maxOpen int

	nextSID int64
	lastNow int64

	sessions map[int64]*session
	tenants  map[string]*tenantState
	heap     *expiryHeap
	popped   int
}

type tenantState struct {
	open  int
	sizes map[string]int64
}

// expiredSet 是判定阶段试探弹出但尚未落地的到期会话索引。
type expiredSet struct {
	is       map[int64]bool
	count    map[string]int
	reserved map[string]int64
}

const maxChunkLen = 1 << 20

func New(t int64, maxOpen int, sink journal.Sink) (*Service, error) {
	if t < 1 || t > 1e9 || maxOpen < 1 || maxOpen > 1000 || sink == nil {
		return nil, ErrInvalid
	}
	s := &Service{
		ledger:   quota.New(),
		sink:     sink,
		t:        t,
		maxOpen:  maxOpen,
		sessions: map[int64]*session{},
		tenants:  map[string]*tenantState{},
	}
	s.heap = newExpiryHeap(s)
	return s, nil
}

func (s *Service) SetQuota(tenant string, q int64) error {
	if tenant == "" || q < 0 || q > 1e15 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.ledger.Info(tenant)
	if info.Used+info.Reserved > q {
		return ErrQuotaBelow
	}
	rec := journal.Record{Op: journal.OpSetQuota, Tenant: tenant, Quota: q}
	if err := s.sink.Append(rec); err != nil {
		return mapJournalError(err)
	}
	if err := s.applySetQuota(tenant, q); err != nil {
		return err
	}
	s.tenant(tenant)
	return nil
}

func (s *Service) Create(tenant, key string, total, now int64) (int64, error) {
	if tenant == "" || key == "" || total < 1 || total > 1e12 || now < 0 || now > 1e12 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return 0, ErrClockBack
	}
	// 判定阶段：先按本次 now 试探弹出，使容量判定基于回收后视图。
	trial := s.trialExpire(now)
	expired := s.expiredSet(trial)
	ts := s.tenant(tenant)
	expCount := 0
	expReserved := int64(0)
	if expired != nil {
		expCount = expired.count[tenant]
		expReserved = expired.reserved[tenant]
	}
	if ts.open-expCount >= s.maxOpen {
		s.restore(trial)
		return 0, ErrTooMany
	}
	info := s.ledger.Info(tenant)
	if info.Used+info.Reserved-expReserved+total > info.Quota {
		s.restore(trial)
		return 0, ErrQuota
	}
	rec := journal.Record{
		Op:     journal.OpCreate,
		Tenant: tenant,
		Key:    key,
		Total:  total,
		Now:    now,
	}
	if err := s.sink.Append(rec); err != nil {
		s.restore(trial)
		return 0, mapJournalError(err)
	}
	s.landExpire(trial, now)
	sid := s.nextSID + 1
	s.nextSID = sid
	sess := &session{
		id:      sid,
		tenant:  tenant,
		key:     key,
		total:   total,
		expiry:  now + s.t,
		state:   StateOpen,
		heapIdx: -1,
	}
	s.sessions[sid] = sess
	ts.open++
	if err := s.ledger.Reserve(tenant, total); err != nil {
		return 0, mapQuotaError(err)
	}
	s.advanceClock(now)
	s.heap.push(sid)
	return sid, nil
}

func (s *Service) Chunk(sid, off int64, data []byte, now int64) error {
	if off < 0 || off > 1e12 || len(data) > maxChunkLen || now < 0 || now > 1e12 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockBack
	}
	trial := s.trialExpire(now)
	sess, err := s.classify(sid, s.expiredSet(trial))
	if err != nil {
		s.restore(trial)
		return err
	}
	end := off + int64(len(data))
	if off > sess.c {
		s.restore(trial)
		return ErrGap
	}
	if end > sess.total {
		s.restore(trial)
		return ErrBeyondTotal
	}
	for i := range data {
		pos := off + int64(i)
		if pos < sess.c && data[i] != sess.data[pos] {
			s.restore(trial)
			return ErrConflict
		}
	}
	rec := journal.Record{
		Op:     journal.OpChunk,
		Tenant: sess.tenant,
		Key:    sess.key,
		SID:    sid,
		Off:    off,
		Data:   append([]byte(nil), data...),
		Now:    now,
	}
	if err := s.sink.Append(rec); err != nil {
		s.restore(trial)
		return mapJournalError(err)
	}
	s.landExpire(trial, now)
	if end > int64(len(sess.data)) {
		grown := make([]byte, end)
		copy(grown, sess.data)
		sess.data = grown
	}
	copy(sess.data[off:end], data)
	if end > sess.c {
		sess.c = end
		sess.expiry = now + s.t
		s.heap.fix(sid)
	}
	s.advanceClock(now)
	return nil
}

func (s *Service) Complete(sid, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockBack
	}
	trial := s.trialExpire(now)
	sess, err := s.classify(sid, s.expiredSet(trial))
	if err != nil {
		s.restore(trial)
		return err
	}
	if sess.c != sess.total {
		s.restore(trial)
		return &IncompleteError{Remaining: sess.total - sess.c}
	}
	rec := journal.Record{
		Op:     journal.OpComplete,
		Tenant: sess.tenant,
		Key:    sess.key,
		SID:    sid,
		Now:    now,
	}
	if err := s.sink.Append(rec); err != nil {
		s.restore(trial)
		return mapJournalError(err)
	}
	s.landExpire(trial, now)
	oldSize := s.finishOpen(sess)
	sess.state = StateComplete
	s.ledger.Commit(sess.tenant, sess.total, oldSize)
	s.tenant(sess.tenant).sizes[sess.key] = sess.total
	s.advanceClock(now)
	return nil
}

func (s *Service) Abort(sid, now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockBack
	}
	trial := s.trialExpire(now)
	sess, err := s.classify(sid, s.expiredSet(trial))
	if err != nil {
		s.restore(trial)
		return err
	}
	rec := journal.Record{
		Op:     journal.OpAbort,
		Tenant: sess.tenant,
		Key:    sess.key,
		SID:    sid,
		Now:    now,
	}
	if err := s.sink.Append(rec); err != nil {
		s.restore(trial)
		return mapJournalError(err)
	}
	s.landExpire(trial, now)
	s.finishOpen(sess)
	sess.state = StateAborted
	s.ledger.Release(sess.tenant, sess.total)
	s.advanceClock(now)
	return nil
}

func (s *Service) Query(sid, now int64) (committed, expiry int64, err error) {
	if now < 0 || now > 1e12 {
		return 0, 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return 0, 0, ErrClockBack
	}
	sess, ok := s.sessions[sid]
	if !ok {
		return 0, 0, ErrUnknownSID
	}
	if sess.state == StateComplete || sess.state == StateAborted {
		return 0, 0, ErrSessionDone
	}
	if sess.state == StateExpired || sess.expiry <= now {
		return 0, 0, ErrExpired
	}
	return sess.c, sess.expiry, nil
}

func (s *Service) Popped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.popped
}

func (s *Service) tenant(t string) *tenantState {
	ts := s.tenants[t]
	if ts == nil {
		ts = &tenantState{sizes: map[string]int64{}}
		s.tenants[t] = ts
	}
	return ts
}

func (s *Service) advanceClock(now int64) {
	if now > s.lastNow {
		s.lastNow = now
	}
}

// classify 按 未知 > 已完成或已中止 > 已到期 的次序判定会话；
// expired 是判定阶段试探弹出（尚未落地）的到期会话集合。
func (s *Service) classify(sid int64, expired *expiredSet) (*session, error) {
	sess, ok := s.sessions[sid]
	if !ok {
		return nil, ErrUnknownSID
	}
	if sess.state == StateComplete || sess.state == StateAborted {
		return nil, ErrSessionDone
	} else if sess.state == StateExpired || (expired != nil && expired.is[sid]) {
		return nil, ErrExpired
	}
	return sess, nil
}

// finishOpen 将开启中会话从堆中原位删除并递减开启计数，返回同键旧对象大小。
func (s *Service) finishOpen(sess *session) int64 {
	s.heap.removeAt(sess.heapIdx)
	ts := s.tenant(sess.tenant)
	ts.open--
	return ts.sizes[sess.key]
}

// trialExpire 在判定阶段试探弹出到期会话；只动堆，不改状态、预留与 popped。
func (s *Service) trialExpire(now int64) []int64 {
	var trial []int64
	for s.heap.Len() > 0 {
		top := s.heap.sids[0]
		if s.sessions[top].expiry > now {
			break
		}
		trial = append(trial, s.heap.pop())
	}
	return trial
}

func (s *Service) expiredSet(trial []int64) *expiredSet {
	if len(trial) == 0 {
		return nil
	}
	e := &expiredSet{
		is:       make(map[int64]bool, len(trial)),
		count:    map[string]int{},
		reserved: map[string]int64{},
	}
	for _, sid := range trial {
		sess := s.sessions[sid]
		e.is[sid] = true
		e.count[sess.tenant]++
		e.reserved[sess.tenant] += sess.total
	}
	return e
}

func (s *Service) restore(trial []int64) {
	for _, sid := range trial {
		s.heap.push(sid)
	}
}

// landExpire 落地判定阶段的试探弹出：试探弹出的会话在操作接受时计入 popped；
// 至多再多弹 1 个（未到期堆顶）作为试探边界并立即放回；随后统一释放预留、
// 置为已到期。被拒绝操作调用 restore，不会走到这里，故 popped 只统计接受操作。
func (s *Service) landExpire(trial []int64, now int64) {
	s.popped += len(trial)
	if s.heap.Len() > 0 {
		s.popped++
		// 判定阶段已排空所有到期会话，堆顶必未到期：弹出探界后立即放回。
		sid := s.heap.pop()
		if s.sessions[sid].expiry <= now {
			trial = append(trial, sid)
		} else {
			s.heap.push(sid)
		}
	}
	for _, sid := range trial {
		sess := s.sessions[sid]
		sess.state = StateExpired
		s.tenant(sess.tenant).open--
		s.ledger.Release(sess.tenant, sess.total)
	}
}

func mapQuotaError(err error) error {
	switch err {
	case quota.ErrInsufficient:
		return ErrQuota
	case quota.ErrBelowOccupied:
		return ErrQuotaBelow
	case quota.ErrInvalidQuota:
		return ErrInvalid
	default:
		return err
	}
}

func mapJournalError(err error) error {
	if err == journal.ErrAppendFailed {
		return ErrJournal
	}
	return err
}

// ---------- 错误 ----------

var (
	ErrInvalid     = errors.New("参数非法")
	ErrClockBack   = errors.New("时钟回退")
	ErrTooMany     = errors.New("会话数超限")
	ErrQuota       = errors.New("额度不足")
	ErrJournal     = errors.New("日志失败")
	ErrUnknownSID  = errors.New("未知会话")
	ErrSessionDone = errors.New("会话已完成或已中止")
	ErrExpired     = errors.New("会话已到期")
	ErrGap         = errors.New("缺口")
	ErrBeyondTotal = errors.New("越界")
	ErrConflict    = errors.New("冲突")
	ErrQuotaBelow  = errors.New("低于占用")
)

// IncompleteError 携带剩余字节 total-c。
type IncompleteError struct {
	Remaining int64
}

func (e *IncompleteError) Error() string {
	return "未完成"
}

// ---------- 到期最小堆 ----------

// expiryHeap 按到期时刻排序（次键 sid），元素为开启中会话 sid。
// 使用 container/heap 的 Push/Pop/Fix/Remove 原位更新与删除，不做惰性删除。
type expiryHeap struct {
	sids []int64
	svc  *Service
}

func newExpiryHeap(svc *Service) *expiryHeap {
	h := &expiryHeap{svc: svc}
	heap.Init(h)
	return h
}

func (h *expiryHeap) Len() int { return len(h.sids) }

func (h *expiryHeap) Less(i, j int) bool {
	a := h.svc.sessions[h.sids[i]]
	b := h.svc.sessions[h.sids[j]]
	if a.expiry != b.expiry {
		return a.expiry < b.expiry
	}
	return a.id < b.id
}

func (h *expiryHeap) Swap(i, j int) {
	h.sids[i], h.sids[j] = h.sids[j], h.sids[i]
	h.svc.sessions[h.sids[i]].heapIdx = i
	h.svc.sessions[h.sids[j]].heapIdx = j
}

func (h *expiryHeap) Push(x any) {
	sid := x.(int64)
	h.svc.sessions[sid].heapIdx = len(h.sids)
	h.sids = append(h.sids, sid)
}

func (h *expiryHeap) Pop() any {
	n := len(h.sids)
	sid := h.sids[n-1]
	h.sids = h.sids[:n-1]
	h.svc.sessions[sid].heapIdx = -1
	return sid
}

func (h *expiryHeap) push(sid int64) { heap.Push(h, sid) }

func (h *expiryHeap) fix(sid int64) {
	heap.Fix(h, h.svc.sessions[sid].heapIdx)
}

func (h *expiryHeap) removeAt(idx int) int64 {
	return heap.Remove(h, idx).(int64)
}

func (h *expiryHeap) pop() int64 { return heap.Pop(h).(int64) }

// ---------- 日志回放 ----------

// Replay 用前 k 条记录重建服务；重建状态与前 k 个被接受操作后逐字段相同。
func Replay(t int64, maxOpen int, recs []journal.Record) (*Service, error) {
	s, err := New(t, maxOpen, journal.NewMemorySink())
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if err := s.applyRecord(r); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) applySetQuota(tenant string, q int64) error {
	return mapQuotaError(s.ledger.SetQuota(tenant, q))
}

// applyRecord 回放单条已接受记录：同样先判定回收、再落地、再生效。
func (s *Service) applyRecord(r journal.Record) error {
	switch r.Op {
	case journal.OpSetQuota:
		return s.applySetQuota(r.Tenant, r.Quota)
	case journal.OpCreate:
		return s.applyCreate(r)
	case journal.OpChunk:
		return s.applyChunk(r)
	case journal.OpComplete:
		return s.applyComplete(r)
	case journal.OpAbort:
		return s.applyAbort(r)
	default:
		return ErrInvalid
	}
}

func (s *Service) applyCreate(r journal.Record) error {
	trial := s.trialExpire(r.Now)
	s.landExpire(trial, r.Now)
	sid := s.nextSID + 1
	s.nextSID = sid
	sess := &session{
		id:      sid,
		tenant:  r.Tenant,
		key:     r.Key,
		total:   r.Total,
		expiry:  r.Now + s.t,
		state:   StateOpen,
		heapIdx: -1,
	}
	s.sessions[sid] = sess
	ts := s.tenant(r.Tenant)
	ts.open++
	if err := s.ledger.Reserve(r.Tenant, r.Total); err != nil {
		return mapQuotaError(err)
	}
	s.advanceClock(r.Now)
	s.heap.push(sid)
	return nil
}

func (s *Service) applyChunk(r journal.Record) error {
	trial := s.trialExpire(r.Now)
	s.landExpire(trial, r.Now)
	sess := s.sessions[r.SID]
	end := r.Off + int64(len(r.Data))
	if end > int64(len(sess.data)) {
		grown := make([]byte, end)
		copy(grown, sess.data)
		sess.data = grown
	}
	copy(sess.data[r.Off:end], r.Data)
	if end > sess.c {
		sess.c = end
		sess.expiry = r.Now + s.t
		s.heap.fix(r.SID)
	}
	s.advanceClock(r.Now)
	return nil
}

func (s *Service) applyComplete(r journal.Record) error {
	trial := s.trialExpire(r.Now)
	s.landExpire(trial, r.Now)
	sess := s.sessions[r.SID]
	oldSize := s.finishOpen(sess)
	sess.state = StateComplete
	s.ledger.Commit(sess.tenant, sess.total, oldSize)
	s.tenant(sess.tenant).sizes[sess.key] = sess.total
	s.advanceClock(r.Now)
	return nil
}

func (s *Service) applyAbort(r journal.Record) error {
	trial := s.trialExpire(r.Now)
	s.landExpire(trial, r.Now)
	sess := s.sessions[r.SID]
	s.finishOpen(sess)
	sess.state = StateAborted
	s.ledger.Release(sess.tenant, sess.total)
	s.advanceClock(r.Now)
	return nil
}
