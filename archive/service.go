package archive

import (
	"fmt"
	"sort"
	"sync"
)

type volume struct {
	id          string
	class       Classification
	status      VolumeStatus
	loan        *LoanInfo
	hold        *HoldInfo
	queue       reservationQueue
	sealPending bool
	scanSteps   int // 最近一次分配扫描访问的存活节点数（开销可验证）
}

type user struct {
	id           string
	maxClass     Classification
	status       UserStatus
	overdueTotal int
	lastReturn   int
	activeLoans  int
}

// Service 是机关档案借阅与预约服务。所有方法可并发调用，互斥串行化。
type Service struct {
	mu      sync.Mutex
	cfg     Config
	lastNow int
	volumes map[string]*volume
	users   map[string]*user
}

// NewService 创建服务。配置非法时 panic（初始化错误属于编程错误）。
func NewService(cfg Config) *Service {
	if cfg.PickupDeadlineDays < 0 || cfg.RenewWindowDays < 0 ||
		cfg.MaxRenewals < 0 || cfg.OverdueThreshold <= 0 || cfg.CooldownDays < 0 {
		panic(fmt.Sprintf("invalid config: %+v", cfg))
	}
	for _, d := range cfg.LoanDays {
		if d <= 0 {
			panic("loan days must be positive")
		}
	}
	return &Service{cfg: cfg, volumes: map[string]*volume{}, users: map[string]*user{}}
}

// AddUser / AddVolume 是初始化型管理操作，不参与时钟推进。
func (s *Service) AddUser(id string, maxClass Classification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || !maxClass.Valid() {
		panic("invalid user")
	}
	s.users[id] = &user{id: id, maxClass: maxClass, status: UserActive}
}

func (s *Service) AddVolume(id string, class Classification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || !class.Valid() {
		panic("invalid volume")
	}
	s.volumes[id] = &volume{id: id, class: class, status: VolInLibrary}
}

func bad(ec ErrorCode, reason string) Outcome { return Outcome{OK: false, Err: ec, Reason: reason} }

func okOut(reason string) Outcome { return Outcome{OK: true, Err: OK, Reason: reason} }

func (s *Service) checkClock(now int) Outcome {
	if now < s.lastNow {
		return bad(ErrClockRollback, fmt.Sprintf("now=%d < last_now=%d", now, s.lastNow))
	}
	return okOut("")
}

func (s *Service) resolve(userID, volumeID string) (*user, *volume, Outcome) {
	u, uok := s.users[userID]
	v, vok := s.volumes[volumeID]
	if !uok || !vok {
		return nil, nil, bad(ErrNotFound, "user or volume not found")
	}
	return u, v, okOut("")
}

// tick 推进确定性时间：先解除满足条件的暂停，再处理各卷待取到期放弃。
// 仅在操作通过全部校验、即将生效前调用；被拒绝操作既不推进时钟也不产生副作用。
func (s *Service) tick(now int) {
	if now < s.lastNow {
		return
	}
	s.lastNow = now
	for _, u := range s.usersByID() {
		if u.status == UserSuspended && u.activeLoans == 0 &&
			now-u.lastReturn >= s.cfg.CooldownDays {
			u.status = UserActive
			u.overdueTotal = 0
		}
	}
	for _, v := range s.volumesByID() {
		s.processVolume(v, now)
	}
}

func (s *Service) usersByID() []*user {
	out := make([]*user, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (s *Service) volumesByID() []*volume {
	out := make([]*volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (s *Service) processVolume(v *volume, now int) {
	if v.hold != nil && now > v.hold.DeadlineDay {
		s.releaseHold(v, now)
	}
}

// releaseHold 放弃当前待取（失去排队位置）并继续级联分配。
func (s *Service) releaseHold(v *volume, now int) {
	v.hold = nil
	if v.sealPending {
		v.sealPending = false
		v.status = VolSealed
		v.queue.clear()
		return
	}
	s.assignFromQueue(v, now)
}

// assignFromQueue 从队首起选择第一个资格满足且状态正常的预约者。
// 不满足者（密级被下调或暂停冻结）被跳过但保留原序位；扫描时物理摘除死节点，
// 扫描步数只取决于当前存活队列长度，与该卷历史预约总数无关。
func (s *Service) assignFromQueue(v *volume, now int) {
	v.scanSteps = 0
	e := v.queue.head
	for e != nil {
		v.scanSteps++
		u := s.users[e.userID]
		if u != nil && u.status == UserActive && u.maxClass >= v.class {
			v.hold = &HoldInfo{UserID: e.userID, AssignedDay: now,
				DeadlineDay: now + s.cfg.PickupDeadlineDays}
			v.status = VolLent
			v.queue.remove(e) // 分配即出队：队列只保留尚未被分配的等待者
			return
		}
		e = e.next
	}
	v.status = VolInLibrary
}
