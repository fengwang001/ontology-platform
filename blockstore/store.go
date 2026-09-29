package blockstore

import (
	"slices"
	"sync"
)

// Digest 是块的内容摘要（调用方负责按内容计算，例如 SHA-256 十六进制串）。
type Digest string

// Manifest 是一次原子提交产生的快照清单。
type Manifest struct {
	ID     string
	Blocks []Digest
}

// GCRound1Result 是第一轮回收的判定结果。
type GCRound1Result struct {
	CycleID int
	Pending []Digest // 被标记为待删的块
	Witness []string // 第一轮开始时仍在进行的会话
	Skipped bool     // 已有回收轮次进行中
}

// GCRound2Result 是第二轮回收的判定结果。
type GCRound2Result struct {
	CycleID     int
	Deleted     []Digest // 本轮真正删除的块
	Restored    []Digest // 被新清单引用而恢复为正常的待删块
	Skipped     bool     // 见证会话尚未全部结束，本轮什么都不删
	WitnessLeft []string
}

type blockEntry struct {
	state BlockState
	data  []byte
}

const (
	sessionOpen      = 0
	sessionCommitted = 1
	sessionEnded     = 2
)

type session struct{ state int }

type storedManifest struct{ blocks []Digest }

type gcCycle struct {
	id      int
	pending map[Digest]bool // 本轮标记为待删的块
	witness map[string]bool // 第一轮开始时仍在进行的会话
}

// Store 是内容寻址去重块库，所有方法可被并发调用。
type Store struct {
	mu        sync.RWMutex
	blocks    map[Digest]*blockEntry
	manifests map[string]storedManifest
	sessions  map[string]*session
	used      int
	capacity  int // <=0 表示不限容量
	cycle     *gcCycle
	nextCycle int
	log       eventLogger
}

// New 创建一个块库。capacity<=0 表示不限制容量。
func New(capacity int) *Store { return NewWithLogger(capacity, newDefaultLogger()) }

// NewWithLogger 使用指定日志器创建块库（日志含每次操作的输入、输出与判定依据）。
func NewWithLogger(capacity int, logger Logger) *Store {
	if logger == nil {
		logger = discardLogger{}
	}
	return &Store{
		blocks:    make(map[Digest]*blockEntry),
		manifests: make(map[string]storedManifest),
		sessions:  make(map[string]*session),
		capacity:  capacity,
		log:       eventLogger{next: logger},
	}
}

func sortedDigits(set map[Digest]bool) []Digest {
	out := make([]Digest, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

func sortedSessions(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// referencedBlocks 必须在持锁时调用：所有已提交清单引用块的并集。
func (s *Store) referencedBlocks() map[Digest]bool {
	refs := make(map[Digest]bool)
	for _, m := range s.manifests {
		for _, d := range m.blocks {
			refs[d] = true
		}
	}
	return refs
}

// BeginSession 开启一个写入会话。
func (s *Store) BeginSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &session{state: sessionOpen}
	s.log.logf("BeginSession", "in={session:%q} out=ok decision=session-created", id)
}

// Upload 上传块；块已存在（含待删）则复用，复用待删块时立即恢复为正常。
func (s *Store) Upload(sessionID string, d Digest, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		s.log.logf("Upload", "in={session:%q digest:%q size:%d} out=%v decision=session-missing", sessionID, d, len(data), ErrSessionNotFound)
		return &CommitError{Reason: ErrSessionNotFound, Detail: sessionID}
	}
	if sess.state == sessionEnded {
		s.log.logf("Upload", "in={session:%q digest:%q size:%d} out=%v decision=session-ended", sessionID, d, len(data), ErrSessionClosed)
		return &CommitError{Reason: ErrSessionClosed, Detail: sessionID}
	}

	if existing, ok := s.blocks[d]; ok {
		restored := false
		if existing.state == StatePending {
			existing.state = StateNormal
			if s.cycle != nil {
				delete(s.cycle.pending, d)
			}
			restored = true
		}
		s.log.logf("Upload", "in={session:%q digest:%q size:%d} out=reused decision=content-exists restored=%v", sessionID, d, len(data), restored)
		return nil
	}

	if s.capacity > 0 && s.used+len(data) > s.capacity {
		s.log.logf("Upload", "in={session:%q digest:%q size:%d} out=%v decision=capacity-full used=%d capacity=%d", sessionID, d, len(data), ErrCapacityFull, s.used, s.capacity)
		return &CommitError{Reason: ErrCapacityFull}
	}

	stored := make([]byte, len(data))
	copy(stored, data)
	s.blocks[d] = &blockEntry{state: StateNormal, data: stored}
	s.used += len(stored)
	s.log.logf("Upload", "in={session:%q digest:%q size:%d} out=stored decision=content-new used=%d", sessionID, d, len(stored), s.used)
	return nil
}

// Commit 原子提交清单；任一前置条件不满足则整体拒绝，不留清单、不改块状态。
func (s *Store) Commit(sessionID, manifestID string, blocks []Digest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.logf("Commit", "in={session:%q manifest:%q blocks:%v}", sessionID, manifestID, blocks)

	sess, ok := s.sessions[sessionID]
	switch {
	case !ok:
		s.log.logf("Commit", "out=%v decision=session-missing", ErrSessionNotFound)
		return &CommitError{Reason: ErrSessionNotFound, Detail: sessionID}
	case sess.state == sessionEnded:
		s.log.logf("Commit", "out=%v decision=session-ended", ErrSessionClosed)
		return &CommitError{Reason: ErrSessionClosed, Detail: sessionID}
	case sess.state == sessionCommitted:
		s.log.logf("Commit", "out=%v decision=duplicate-commit", ErrAlreadyCommitted)
		return &CommitError{Reason: ErrAlreadyCommitted, Detail: sessionID}
	}

	if _, dup := s.manifests[manifestID]; dup {
		s.log.logf("Commit", "out=%v decision=manifest-id-exists", ErrAlreadyCommitted)
		return &CommitError{Reason: ErrAlreadyCommitted, Detail: "manifest id " + manifestID}
	}

	seen := make(map[Digest]bool, len(blocks))
	for _, d := range blocks {
		seen[d] = true
		b, exists := s.blocks[d]
		if !exists || b.state == StateDeleted {
			s.log.logf("Commit", "out=%v decision=missing-block digest=%q", ErrBlockMissing, d)
			return &CommitError{Reason: ErrBlockMissing, Detail: string(d)}
		}
	}

	manifestBlocks := make([]Digest, len(blocks))
	copy(manifestBlocks, blocks)
	s.manifests[manifestID] = storedManifest{blocks: manifestBlocks}
	sess.state = sessionCommitted

	var restored []Digest
	for d := range seen {
		if b, ok := s.blocks[d]; ok && b.state == StatePending {
			b.state = StateNormal
			if s.cycle != nil {
				delete(s.cycle.pending, d)
			}
			restored = append(restored, d)
		}
	}
	slices.Sort(restored)
	s.log.logf("Commit", "out=committed decision=all-blocks-present restored=%v", restored)
	return nil
}

// EndSession 结束会话（重复结束幂等）。
func (s *Store) EndSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.log.logf("EndSession", "in={session:%q} out=%v decision=session-missing", sessionID, ErrSessionNotFound)
		return &CommitError{Reason: ErrSessionNotFound, Detail: sessionID}
	}
	decision := "ended"
	if sess.state == sessionEnded {
		decision = "already-ended"
	}
	sess.state = sessionEnded
	s.log.logf("EndSession", "in={session:%q} out=ok decision=%s", sessionID, decision)
	return nil
}

// Read 按摘要读取块；待删块在被物理删除前仍可完整读回。
func (s *Store) Read(d Digest) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blocks[d]
	if !ok || b.state == StateDeleted {
		s.log.logf("Read", "in={digest:%q} out=%v decision=not-found", d, ErrBlockMissing)
		return nil, &CommitError{Reason: ErrBlockMissing, Detail: string(d)}
	}
	data := make([]byte, len(b.data))
	copy(data, b.data)
	s.log.logf("Read", "in={digest:%q} out=%d-bytes decision=hit state=%s", d, len(data), b.state)
	return data, nil
}

// ReadManifest 读回清单；不变式保证其引用的每个块此刻都存在且可读。
func (s *Store) ReadManifest(manifestID string) (*Manifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.manifests[manifestID]
	if !ok {
		s.log.logf("ReadManifest", "in={manifest:%q} out=%v decision=not-found", manifestID, ErrManifestNotFound)
		return nil, &CommitError{Reason: ErrManifestNotFound, Detail: manifestID}
	}
	blocks := make([]Digest, len(m.blocks))
	copy(blocks, m.blocks)
	for _, d := range blocks {
		b, exists := s.blocks[d]
		if !exists || b.state == StateDeleted {
			s.log.logf("ReadManifest", "in={manifest:%q} out=%v decision=dangling-ref digest=%q", manifestID, ErrBlockMissing, d)
			return nil, &CommitError{Reason: ErrBlockMissing, Detail: string(d)}
		}
	}
	s.log.logf("ReadManifest", "in={manifest:%q} out=%d-blocks decision=all-readable", manifestID, len(blocks))
	return &Manifest{ID: manifestID, Blocks: blocks}, nil
}

// StateOf 返回块当前状态（库中不存在时返回 StateDeleted）。
func (s *Store) StateOf(d Digest) BlockState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if b, ok := s.blocks[d]; ok {
		return b.state
	}
	return StateDeleted
}

// GCRound1 第一轮：把未被任何已提交清单引用的正常块标记为待删，
// 并快照此刻仍在进行的会话作为见证集合；待删块不删除、仍可读。
func (s *Store) GCRound1() (GCRound1Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cycle != nil {
		s.log.logf("GCRound1", "out=%v decision=cycle-active id=%d", ErrGCInProgress, s.cycle.id)
		return GCRound1Result{Skipped: true, CycleID: s.cycle.id}, &CommitError{Reason: ErrGCInProgress}
	}

	refs := s.referencedBlocks()
	cycle := &gcCycle{
		id:      s.nextCycle,
		pending: make(map[Digest]bool),
		witness: make(map[string]bool),
	}
	s.nextCycle++

	for d, b := range s.blocks {
		if b.state == StateNormal && !refs[d] {
			b.state = StatePending
			cycle.pending[d] = true
		}
	}
	for id, sess := range s.sessions {
		if sess.state != sessionEnded {
			cycle.witness[id] = true
		}
	}
	s.cycle = cycle

	res := GCRound1Result{
		CycleID: cycle.id,
		Pending: sortedDigits(cycle.pending),
		Witness: sortedSessions(cycle.witness),
	}
	s.log.logf("GCRound1", "out={cycle:%d pending:%v witness:%v} decision=mark-unreferenced refs=%d", cycle.id, res.Pending, res.Witness, len(refs))
	return res, nil
}

// GCRound2 第二轮：见证会话全部结束时，删除仍无任何清单引用的待删块，
// 被新清单引用（或被会话上传复用）的恢复为正常；只要还有见证会话未结束，本轮什么都不删。
func (s *Store) GCRound2() (GCRound2Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cycle == nil {
		s.log.logf("GCRound2", "out=%v decision=no-cycle", ErrGCInProgress)
		return GCRound2Result{}, &CommitError{Reason: ErrGCInProgress, Detail: "no active cycle"}
	}
	cycle := s.cycle

	left := make(map[string]bool)
	for id := range cycle.witness {
		sess, ok := s.sessions[id]
		if !ok || sess.state != sessionEnded {
			left[id] = true
		}
	}
	res := GCRound2Result{CycleID: cycle.id, WitnessLeft: sortedSessions(left)}

	if len(left) > 0 {
		res.Skipped = true
		s.log.logf("GCRound2", "out={cycle:%d skipped:true witness-left:%v} decision=witnesses-open delete-nothing", cycle.id, res.WitnessLeft)
		return res, nil
	}

	refs := s.referencedBlocks()
	var deleted, restored []Digest
	// 只裁决本轮 pending 集合中仍处待删状态的块；已被上传/提交恢复的块已移出集合。
	for d := range cycle.pending {
		b, ok := s.blocks[d]
		if !ok || b.state != StatePending {
			continue
		}
		if refs[d] {
			b.state = StateNormal
			restored = append(restored, d)
		} else {
			s.used -= len(b.data)
			delete(s.blocks, d)
			deleted = append(deleted, d)
		}
	}
	slices.Sort(deleted)
	slices.Sort(restored)
	res.Deleted = deleted
	res.Restored = restored
	s.cycle = nil
	s.log.logf("GCRound2", "out={cycle:%d deleted:%v restored:%v} decision=witnesses-closed recheck-refs", cycle.id, deleted, restored)
	return res, nil
}
