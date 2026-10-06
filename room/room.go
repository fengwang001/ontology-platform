// Package room 实现抢救位与诊室的占用与叫号，并组合 queue 与 triage
// 构成完整的急诊预检分诊系统。所有公共操作经互斥锁串行化，
// 并发调用等价于某个串行顺序。
package room

import (
	"errors"
	"sync"

	"ontology/queue"
	"ontology/triage"
)

// 拒绝原因，按判定优先级：参数非法 > 时钟回退 > 存在性 > 状态不符 > 无可叫者。
var (
	ErrInvalidParam = queue.ErrInvalidParam
	ErrClock        = errors.New("room: clock regression")
	ErrExistence    = queue.ErrExistence
	ErrState        = queue.ErrState
	ErrNoCallable   = errors.New("room: no callable patient")
)

const maxNow = 1_000_000_000 // 时刻上界（分钟）

// Kind 为房间类型。
type Kind int

const (
	Resuscitation Kind = 1 // 抢救位：候选为 1、2 级
	Clinic        Kind = 2 // 诊室：候选为 2、3、4 级
)

func (k Kind) valid() bool { return k == Resuscitation || k == Clinic }

// levels 返回该房型可叫号的候选等级。
func (k Kind) levels() []int {
	if k == Resuscitation {
		return []int{1, 2}
	}
	return []int{2, 3, 4}
}

// Room 为一个抢救位或诊室，至多占一名患者。
type Room struct {
	ID      string
	Kind    Kind
	Patient string // 占用者标识，空串为空闲
}

// System 为分诊叫号系统。
type System struct {
	mu     sync.Mutex
	q      *queue.Queue
	maxNow int // 已接受操作的最大 now
	rooms  map[string]*Room
	roomOf map[string]string // 占用患者 -> 房间
}

// New 创建系统；r1..r4 为四个等级的复评时限，a 为到诊宽限，各为 1..10^4。
func New(r1, r2, r3, r4, a int) (*System, error) {
	q, err := queue.NewQueue(r1, r2, r3, r4, a)
	if err != nil {
		return nil, err
	}
	return &System{
		q:      q,
		maxNow: -1,
		rooms:  make(map[string]*Room),
		roomOf: make(map[string]string),
	}, nil
}

// checkNow 校验时刻：先参数非法（越界），后时钟回退。
func (s *System) checkNow(now int) error {
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

// settle 在操作被接受后、自身判定前落地全部过号并推进时钟。
func (s *System) settle(now int) {
	for _, p := range s.q.Settle(now) {
		if rid, ok := s.roomOf[p.ID]; ok {
			s.rooms[rid].Patient = ""
			delete(s.roomOf, p.ID)
		}
	}
	s.maxNow = now
}

// AddRoom 添加房间；kind 须为抢救位或诊室，编号不重复。不携带 now。
func (s *System) AddRoom(id string, kind Kind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || !kind.valid() {
		return ErrInvalidParam
	}
	if _, ok := s.rooms[id]; ok {
		return ErrExistence
	}
	s.rooms[id] = &Room{ID: id, Kind: kind}
	return nil
}

// Register 定级入队。
func (s *System) Register(now int, patient string, v triage.Vitals) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if patient == "" || !v.Valid() {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	if s.q.Has(patient) {
		return ErrExistence
	}
	s.settle(now)
	_, err := s.q.Register(now, patient, v)
	return err
}

// Reassess 复评，仅对候诊者。
func (s *System) Reassess(now int, patient string, v triage.Vitals) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if patient == "" || !v.Valid() {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	if !s.q.Has(patient) {
		return ErrExistence
	}
	s.settle(now)
	_, err := s.q.Reassess(now, patient, v)
	return err
}

// Call 叫号：要求房间空闲，在候选等级中取排序键最小的未逾期者；
// 返回叫到者与按序被跳过的逾期候选。无可叫者时报 ErrNoCallable，房间保持空闲。
func (s *System) Call(now int, roomID string) (called string, skipped []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNow(now); err != nil {
		return "", nil, err
	}
	rm, ok := s.rooms[roomID]
	if !ok {
		return "", nil, ErrExistence
	}
	s.settle(now)
	if rm.Patient != "" {
		return "", nil, ErrState
	}
	chosen, skip := s.q.Select(now, rm.Kind.levels())
	if chosen == nil {
		return "", nil, ErrNoCallable
	}
	rm.Patient = chosen.ID
	s.roomOf[chosen.ID] = roomID
	skipped = make([]string, len(skip))
	for i, p := range skip {
		skipped[i] = p.ID
	}
	return chosen.ID, skipped, nil
}

// Arrive 到诊：要求已叫号，转为就诊中。
func (s *System) Arrive(now int, patient string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if patient == "" {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	if !s.q.Has(patient) {
		return ErrExistence
	}
	s.settle(now)
	return s.q.Arrive(patient)
}

// Finish 完成就诊：要求房内患者就诊中，患者离开、房间空闲。
func (s *System) Finish(now int, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNow(now); err != nil {
		return err
	}
	rm, ok := s.rooms[roomID]
	if !ok {
		return ErrExistence
	}
	s.settle(now)
	if rm.Patient == "" {
		return ErrState
	}
	pid := rm.Patient
	if err := s.q.Finish(pid); err != nil {
		return err
	}
	rm.Patient = ""
	delete(s.roomOf, pid)
	return nil
}
