package dr

import (
	"sort"
	"sync"
)

// Stats 为内部计数器，用于以可验证方式证明性能性质。
type Stats struct {
	DataReads int64 // 用电数据点读取次数
}

// System 为需求响应系统。所有公开方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行执行顺序。
type System struct {
	cfg Config

	mu        sync.Mutex
	last      int64
	clockInit bool

	events     map[string]*Event
	eventOrder []string
	nextID     int

	data      map[string]map[int64]float64 // 参与者 → 间隔起点 → 电量
	watermark map[string]int64             // 参与者 → 已考核事件的最大有效窗口终点
	byPart    map[string]map[string]*Commitment

	Stats Stats
}

// New 校验配置并创建系统。
func New(cfg Config) (*System, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &System{
		cfg:       cfg,
		events:    map[string]*Event{},
		data:      map[string]map[int64]float64{},
		watermark: map[string]int64{},
		byPart:    map[string]map[string]*Commitment{},
	}, nil
}

// Config 返回系统配置。
func (s *System) Config() Config { return s.cfg }

func (s *System) checkClock(now int64) *Error {
	if s.clockInit && now < s.last {
		return newErr(ErrKindClock, "当前时刻 %d 早于上次被接受操作的时刻 %d", now, s.last)
	}
	return nil
}

func (s *System) advance(now int64) {
	s.last = now
	s.clockInit = true
}

// meterGet 读取一个用电数据点；所有用电数据读取都经过此函数以便计数验证。
func (s *System) meterGet(participant string, tick int64) (float64, bool) {
	s.Stats.DataReads++
	m := s.data[participant]
	if m == nil {
		return 0, false
	}
	v, ok := m[tick]
	return v, ok
}

func (s *System) commitmentsOf(participant string) map[string]*Commitment {
	return s.byPart[participant]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
