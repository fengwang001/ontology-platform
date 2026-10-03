// Package ontology 提供轮询同步删除推断器 Syncer，由 scan、absent、guard
// 三个包组成：多轮完整扫描的连续缺席推断删除，删除比例异常时熔断等待人工放行。
package ontology

import (
	"errors"
	"sync"

	"ontology/absent"
	"ontology/guard"
	"ontology/scan"
)

var (
	ErrInvalid    = errors.New("ontology: invalid argument")
	ErrPermission = errors.New("ontology: permission denied")
	ErrClock      = errors.New("ontology: clock rollback")

	ErrBusy       = scan.ErrBusy
	ErrNoSession  = scan.ErrNoSession
	ErrNotSuspect = guard.ErrNotSuspect
)

// Row 是源表的一行 (k, v)。
type Row struct {
	K int64
	V int64
}

// Syncer 是轮询同步删除推断器。所有方法可并发调用，效果等价于某个串行顺序。
type Syncer struct {
	mu      sync.Mutex
	p       int
	k       int
	x       int
	maxNow  int64
	scans   *scan.Manager
	tables  []*absent.Table
	breaker []*guard.Breaker
}

const (
	maxKey   = int64(1_000_000_000)
	maxValue = int64(1_000_000_000)
	maxClock = int64(1_000_000_000_000)
)

// New 构造推断器：P 个分区（1..16）、连续缺席 K 轮（1..10）判定删除、
// 熔断比例百分数 X（1..100）。参数越界返回 ErrInvalid。
func New(p, k, x int) (*Syncer, error) {
	if p < 1 || p > 16 || k < 1 || k > 10 || x < 1 || x > 100 {
		return nil, ErrInvalid
	}
	s := &Syncer{
		p:       p,
		k:       k,
		x:       x,
		scans:   scan.NewManager(p),
		tables:  make([]*absent.Table, p),
		breaker: make([]*guard.Breaker, p),
	}
	for i := 0; i < p; i++ {
		s.tables[i] = absent.NewTable()
		s.breaker[i] = guard.NewBreaker(x)
	}
	return s, nil
}

func (s *Syncer) checkPart(part int) error {
	if part < 0 || part >= s.p {
		return ErrInvalid
	}
	return nil
}

func (s *Syncer) checkNow(now int64) error {
	if now < 0 || now > maxClock {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

// Begin 打开分区 part 的扫描会话，返回新 epoch。
// 拒绝次序：参数非法 → 时钟回退 → ErrBusy。被拒不消耗 epoch。
func (s *Syncer) Begin(part int, now int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkPart(part); err != nil {
		return 0, err
	}
	if err := s.checkNow(now); err != nil {
		return 0, err
	}
	epoch, err := s.scans.Begin(part)
	if err != nil {
		return 0, err
	}
	s.maxNow = now
	return epoch, nil
}

// Report 把一批源表行写入目标表，逐项返回 Insert/Update/Same，
// 并把每个键标为本会话已见（a 归零），即使会话最终不完整。
// 拒绝次序：参数非法（分区、键范围、键属分区、值范围、rows 内键重复）
// → 时钟回退 → ErrNoSession。
func (s *Syncer) Report(part int, epoch int64, rows []Row, now int64) ([]absent.Effect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkPart(part); err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(rows))
	for _, r := range rows {
		if r.K < 1 || r.K > maxKey || int(r.K%int64(s.p)) != part {
			return nil, ErrInvalid
		}
		if r.V < -maxValue || r.V > maxValue {
			return nil, ErrInvalid
		}
		if _, dup := seen[r.K]; dup {
			return nil, ErrInvalid
		}
		seen[r.K] = struct{}{}
	}
	if err := s.checkNow(now); err != nil {
		return nil, err
	}
	if err := s.scans.Check(part, epoch); err != nil {
		return nil, err
	}
	effects := make([]absent.Effect, len(rows))
	for i, r := range rows {
		effects[i] = s.tables[part].Put(r.K, r.V)
		s.scans.Mark(part, r.K)
	}
	s.maxNow = now
	return effects, nil
}

// End 关闭分区 part 的会话。complete 为假时不做任何缺席统计；为真时
// 先给本会话未见的存活键 a 加 1，再按熔断规则处理候选集合 C（a>=K）：
// 未 Suspect 时若 |C|*100 > X*n0 则熔断（一个也不删，返回 Tripped），
// 否则删除 C；已 Suspect 时只累计不删除。返回删除键数与是否触发熔断。
// 拒绝次序：参数非法 → 时钟回退 → ErrNoSession。
func (s *Syncer) End(part int, epoch int64, complete bool, now int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkPart(part); err != nil {
		return 0, false, err
	}
	if err := s.checkNow(now); err != nil {
		return 0, false, err
	}
	if err := s.scans.Check(part, epoch); err != nil {
		return 0, false, err
	}
	seen := s.scans.Close(part)
	s.maxNow = now
	if !complete {
		return 0, false, nil
	}
	tbl := s.tables[part]
	n0 := tbl.Len()
	tbl.AgeExcept(seen)
	if s.breaker[part].Suspect() {
		return 0, false, nil
	}
	c := tbl.Deletable(s.k)
	if s.breaker[part].Evaluate(len(c), n0) {
		return 0, true, nil
	}
	return tbl.Delete(c), false, nil
}

// Approve 人工放行：role 须为 2，分区须为 Suspect。清除 Suspect 并立即
// 删除当前 a>=K 的全部键（不再判熔断），返回删除数。
// 拒绝次序：参数非法 → ErrPermission → 时钟回退 → ErrNotSuspect。
func (s *Syncer) Approve(role, part int, now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkPart(part); err != nil {
		return 0, err
	}
	if role != 2 {
		return 0, ErrPermission
	}
	if err := s.checkNow(now); err != nil {
		return 0, err
	}
	if err := s.breaker[part].Clear(); err != nil {
		return 0, err
	}
	s.maxNow = now
	return s.tables[part].Delete(s.tables[part].Deletable(s.k)), nil
}

// Entry 是目标表中一个键的快照：值与缺席计数。
type Entry struct {
	V int64
	A int
}

// Snapshot 返回分区 part 目标表的完整快照（含每键缺席计数）。
func (s *Syncer) Snapshot(part int) map[int64]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	tbl := s.tables[part]
	out := make(map[int64]Entry, tbl.Len())
	for _, k := range tbl.Keys() {
		v, a, _ := tbl.Get(k)
		out[k] = Entry{V: v, A: a}
	}
	return out
}

// Suspect 报告分区 part 是否处于熔断状态。
func (s *Syncer) Suspect(part int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.breaker[part].Suspect()
}

// Epoch 返回分区 part 当前的 epoch 计数。
func (s *Syncer) Epoch(part int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans.Epoch(part)
}
