package medschedule

import "sync"

const maxNow int64 = 1_000_000_000

// Drug 是药品目录条目。
type Drug struct {
	Category       string
	MinIntervalSec int64 // 最小安全给药间隔（正整数秒）
}

// System 是给药时间表系统。
// 单一读写互斥保证所有并发操作等价于某个串行顺序；
// 每个操作在锁内完成全部校验与状态变更，被拒绝的操作不触碰任何状态。
type System struct {
	mu      sync.RWMutex
	w       int64
	now     int64
	drugs   map[string]*Drug
	allergy map[string]map[string]bool // patient -> 过敏项（药品名或类别）集合
	orders  map[string]*order
	// 每“患者+药品”最近一次实际给药时刻（任意医嘱，含已停嘱与 PRN）。
	lastAdmin map[string]int64
}

// NewSystem 创建系统。W 为按时窗口半宽，必须为正整数秒。
func NewSystem(w int64) (*System, error) {
	if w <= 0 {
		return nil, errInvalid("W must be positive")
	}
	return &System{
		w:         w,
		now:       -1,
		drugs:     map[string]*Drug{},
		allergy:   map[string]map[string]bool{},
		orders:    map[string]*order{},
		lastAdmin: map[string]int64{},
	}, nil
}

// validateCommon 校验公共参数：时间范围、标识非空。
// 调用方必须先通过参数校验，再检查时钟回退，以保证错误优先级。
func validateIDs(ids ...string) bool {
	for _, id := range ids {
		if id == "" {
			return false
		}
	}
	return true
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// checkClock 校验时钟回退，但不推进时钟。
// 时钟只在全部校验通过、操作提交时才推进（commitClock），
// 因此被拒绝的操作天然不改变任何状态与时钟。
func (s *System) checkClock(now int64) error {
	if s.now >= 0 && now < s.now {
		return errRollback("now must not be less than last accepted now")
	}
	return nil
}

func (s *System) commitClock(now int64) { s.now = now }

func patientDrugKey(patient, drug string) string { return patient + "\x00" + drug }

// RegisterDrug 登记药品。重复登记按最新值覆盖（同一标识）。
func (s *System) RegisterDrug(now int64, name, category string, minInterval int64) error {
	if !validNow(now) || !validateIDs(name, category) || minInterval <= 0 {
		return errInvalid("invalid drug parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.drugs[name] = &Drug{Category: category, MinIntervalSec: minInterval}
	s.commitClock(now)
	return nil
}

// SetAllergy 登记/取消患者对某药品或类别的过敏。
func (s *System) SetAllergy(now int64, patient, item string, active bool) error {
	if !validNow(now) || !validateIDs(patient, item) {
		return errInvalid("invalid allergy parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	set := s.allergy[patient]
	if set == nil {
		set = map[string]bool{}
		s.allergy[patient] = set
	}
	if active {
		set[item] = true
	} else {
		delete(set, item)
	}
	s.commitClock(now)
	return nil
}

func (s *System) hasAllergy(patient, drug string) bool {
	d, ok := s.drugs[drug]
	if !ok {
		return false
	}
	set := s.allergy[patient]
	return set[drug] || set[d.Category]
}
