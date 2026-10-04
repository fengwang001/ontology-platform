// Package strike 实现创作者违规计分与账号状态。
//
// 每条生效决定按权重 level-1 给创作者记分，计分在 [t, t+P) 内有效，
// 恰到 t+P 即失效。账号状态是此刻有效且未推翻计分之和 s 的纯函数：
// s<3 正常，3<=s<5 禁言，s>=5 封禁，不粘滞。
//
// Store 同时持有全系统共享的互斥锁与时钟（maxNow），上层的
// decision 与 appeal 包通过 Lock/Unlock 让所有操作整体互斥，
// 从而并发调用等价于某个串行顺序。
package strike

import (
	"errors"
	"sort"
	"sync"
)

// 公共哨兵错误，上层包再导出，均可用 errors.Is 区分。
var (
	ErrInvalidArgument = errors.New("strike: invalid argument")
	ErrClockRegression = errors.New("strike: clock regression")
)

// 构造参数与 now 的合法范围。
const (
	minParam = int64(1)
	maxParam = int64(1e9)
	maxNow   = int64(1e12)
)

// ValidParam 报告构造参数（P/A/Tmax）是否合法。
func ValidParam(v int64) bool { return v >= minParam && v <= maxParam }

// ValidNow 报告操作时刻是否合法。
func ValidNow(now int64) bool { return now >= 0 && now <= maxNow }

// State 为创作者账号状态。
type State int

const (
	StateNormal State = iota // 正常
	StateMuted               // 禁言
	StateBanned              // 封禁
)

func (s State) String() string {
	switch s {
	case StateMuted:
		return "muted"
	case StateBanned:
		return "banned"
	default:
		return "normal"
	}
}

// stateOf 是账号状态的纯函数定义。
func stateOf(sum int) State {
	switch {
	case sum >= 5:
		return StateBanned
	case sum >= 3:
		return StateMuted
	default:
		return StateNormal
	}
}

// scoreRecord 是一条计分记录；被推翻后自始不计。
type scoreRecord struct {
	weight     int
	t          int64
	overturned bool
}

// creatorScores 保存单个创作者的全部计分，按 t 升序（时钟单调，追加即有序）。
type creatorScores struct {
	recs  []*scoreRecord
	times []int64 // 与 recs 平行的时刻序列，供二分定位窗口
}

// Store 是计分存储，也是全系统的共享锁与时钟持有者。
type Store struct {
	mu         sync.Mutex
	period     int64 // 计分有效期 P
	maxNow     int64 // 已接受操作的最大 now
	creators   map[string]*creatorScores
	byDecision map[string]*scoreRecord
	touched    int // 非导出计数器：最近一次 State 查询触碰的计分记录数
}

// NewStore 创建计分存储，period 为计分有效期 P，须在 [1, 1e9]。
func NewStore(period int64) (*Store, error) {
	if !ValidParam(period) {
		return nil, ErrInvalidArgument
	}
	return &Store{
		period:     period,
		maxNow:     -1,
		creators:   make(map[string]*creatorScores),
		byDecision: make(map[string]*scoreRecord),
	}, nil
}

// Lock 获取全局互斥锁。所有上层操作须整体持有该锁。
func (s *Store) Lock() { s.mu.Lock() }

// Unlock 释放全局互斥锁。
func (s *Store) Unlock() { s.mu.Unlock() }

// CheckClock 校验时钟未回退；调用方须持有锁。
func (s *Store) CheckClock(now int64) error {
	if now < s.maxNow {
		return ErrClockRegression
	}
	return nil
}

// AcceptClock 在接受操作后推进时钟；调用方须持有锁。
func (s *Store) AcceptClock(now int64) { s.maxNow = now }

// AddScore 追加一条计分记录；调用方须持有锁。t 全局单调故每创作者有序。
func (s *Store) AddScore(decisionID, creator string, weight int, t int64) {
	rec := &scoreRecord{weight: weight, t: t}
	cs := s.creators[creator]
	if cs == nil {
		cs = &creatorScores{}
		s.creators[creator] = cs
	}
	cs.recs = append(cs.recs, rec)
	cs.times = append(cs.times, t)
	s.byDecision[decisionID] = rec
}

// Overturn 将某决定对应的计分标记为已推翻（自始不计）；调用方须持有锁。
func (s *Store) Overturn(decisionID string) {
	if rec, ok := s.byDecision[decisionID]; ok {
		rec.overturned = true
	}
}

// StateLocked 计算 creator 在 now 时刻的账号状态；调用方须持有锁。
//
// 在 times 上二分定位有效期窗口 (now-P, now]，只触碰窗口内记录，
// 故 touched 不超过有效期内计分条数，与已过期历史及其他创作者无关。
func (s *Store) StateLocked(creator string, now int64) State {
	sum := 0
	if cs := s.creators[creator]; cs != nil {
		lo := sort.Search(len(cs.times), func(i int) bool { return cs.times[i] > now-s.period })
		hi := sort.Search(len(cs.times), func(i int) bool { return cs.times[i] > now })
		for i := lo; i < hi; i++ {
			s.touched++
			if !cs.recs[i].overturned {
				sum += cs.recs[i].weight
			}
		}
	}
	return stateOf(sum)
}

// State 是只读查询：返回 creator 在 now 时刻的账号状态。
// 它是 s 的纯函数，不校验也不推进时钟。
func (s *Store) State(creator []byte, now int64) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.StateLocked(string(creator), now)
}
