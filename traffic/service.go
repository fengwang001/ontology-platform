package traffic

import (
	"math"
	"sort"
	"strconv"
	"sync"
)

// incident 是事件的内部表示。
type incident struct {
	id        int
	link      int
	start     float64
	reduction float64
	ended     bool
}

// segState 是一条路段在锚点 anchorTime 的排队状态。
// 排队长度按 q(anchorTime) + rate*(t-anchorTime) 分段线性演化。
type segState struct {
	anchorTime float64
	q          float64 // 锚点处排队（车辆数）
	rate       float64 // 到达流量 - 有效通行能力
	full       bool    // 是否已回溢（排队达到容量，限制上游）
	cap        float64 // 容量（车辆数），用于上钳
}

func (st *segState) queueAt(t float64) float64 {
	v := st.q + st.rate*(t-st.anchorTime)
	if v < 0 {
		return 0
	}
	if st.full && st.rate >= 0 && v > st.cap {
		return st.cap
	}
	return v
}

// IncidentView 是事件的对外视图。
type IncidentView struct {
	ID        int
	Link      int
	Start     float64
	Reduction float64
	Active    bool
}

// LinkState 是一条路段在当前推进时刻的状态。
type LinkState struct {
	Link         int
	Queue        float64 // 排队长度（车辆数）
	EffectiveCap float64
	Level        int // 0 表示未受影响
}

// Service 是事件驱动的影响传播推演服务。
type Service struct {
	mu      sync.Mutex
	net     *Network
	log     Logger
	now     float64
	n       int
	links   []*Link
	capVeh  []float64 // 每条路段容量（车辆数）
	states  []segState
	inc     map[int]*incident
	pending []int // 尚未到开始时刻的事件 ID，按 start 升序
	nextEq  *equilibrium
	index   map[int]int
}

// New 创建服务。
func New(net *Network, logger Logger) *Service {
	if logger == nil {
		logger = discardLogger{}
	}
	ids := make([]int, 0, len(net.links))
	for id := range net.links {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	index := make(map[int]int, len(ids))
	links := make([]*Link, len(ids))
	capVeh := make([]float64, len(ids))
	states := make([]segState, len(ids))
	for i, id := range ids {
		index[id] = i
		links[i] = net.links[id]
		capVeh[i] = net.capacityInVehicles(net.links[id])
		states[i].cap = capVeh[i]
	}
	s := &Service{
		net:    net,
		log:    logger,
		inc:    make(map[int]*incident),
		links:  links,
		capVeh: capVeh,
		states: states,
		n:      len(ids),
	}
	s.index = index
	s.nextEq = s.computeEquilibrium()
	return s
}

// Query 查询路段当前时刻状态。
func (s *Service) Query(linkID int) (LinkState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.index[linkID]
	if !ok {
		return LinkState{}, ErrLinkNotFound
	}
	st := s.states[idx]
	return LinkState{
		Link:         linkID,
		Queue:        st.queueAt(s.now),
		EffectiveCap: s.nextEq.cap[idx],
		Level:        s.nextEq.level[idx],
	}, nil
}

// Register 在 at 时刻登记一个将于 start 时刻开始、削减比例为 reduction 的事件。
func (s *Service) Register(incidentID, linkID int, at, start, reduction float64) error {
	if !isFinite(at) || !isFinite(start) || !isFinite(reduction) || at < 0 || start < at-eps {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	if at < s.now-eps {
		s.mu.Unlock()
		s.log.Log(LogEntry{Time: at, Op: "register", Output: "rejected", Reason: "clock rewind"})
		return ErrClockRewind
	}
	if _, ok := s.index[linkID]; !ok {
		s.mu.Unlock()
		s.log.Log(LogEntry{Time: at, Op: "register", Output: "rejected", Reason: "link not found"})
		return ErrLinkNotFound
	}
	if _, dup := s.inc[incidentID]; dup {
		s.mu.Unlock()
		s.log.Log(LogEntry{Time: at, Op: "register", Output: "rejected", Reason: "incident duplicate"})
		return ErrInvalidArgument
	}
	if reduction < 0-eps || reduction > 1+eps {
		s.mu.Unlock()
		s.log.Log(LogEntry{Time: at, Op: "register", Output: "rejected", Reason: "reduction out of range"})
		return ErrReductionOutOfRange
	}
	in := &incident{id: incidentID, link: linkID, start: start, reduction: reduction}
	s.advanceTo(at)
	s.inc[incidentID] = in
	if start > s.now+eps {
		s.insertPending(in)
	}
	s.syncRates()
	s.nextEq = s.computeEquilibrium()
	s.resetRates()
	s.mu.Unlock()
	s.log.Log(LogEntry{Time: at, Op: "register", Output: "accepted",
		Input: incidentInput(incidentID, linkID, start, reduction), Reason: "registered"})
	return nil
}

// UpdateReduction 在 at 时刻把事件削减比例改为 reduction。
func (s *Service) UpdateReduction(incidentID int, at, reduction float64) error {
	if !isFinite(at) || !isFinite(reduction) {
		return ErrInvalidArgument
	}
	return s.mutateIncident("update_reduction", incidentID, at, &reduction, func(in *incident) string {
		old := in.reduction
		in.reduction = reduction
		s.syncRates()
		return "reduction " + ftoa(old) + "->" + ftoa(reduction)
	})
}

// Resolve 在 at 时刻解除事件。
func (s *Service) Resolve(incidentID int, at float64) error {
	if !isFinite(at) {
		return ErrInvalidArgument
	}
	return s.mutateIncident("resolve", incidentID, at, nil, func(in *incident) string {
		in.ended = true
		s.removePending(in.id)
		s.syncRates()
		return "resolved"
	})
}

// Advance 仅推进时钟到 at，不登记或修改任何事件。
func (s *Service) Advance(at float64) error {
	if !isFinite(at) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.now-eps {
		s.log.Log(LogEntry{Time: at, Op: "advance", Output: "rejected", Reason: "clock rewind"})
		return ErrClockRewind
	}
	s.advanceTo(at)
	s.syncRates()
	s.nextEq = s.computeEquilibrium()
	s.resetRates()
	s.log.Log(LogEntry{Time: at, Op: "advance", Output: "accepted", Reason: "advanced"})
	return nil
}

// mutateIncident 按固定校验次序执行针对既有事件的变更。
func (s *Service) mutateIncident(op string, incidentID int, at float64, reduction *float64,
	run func(in *incident) string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.now-eps {
		s.log.Log(LogEntry{Time: at, Op: op, Output: "rejected", Reason: "clock rewind"})
		return ErrClockRewind
	}
	in, ok := s.inc[incidentID]
	if !ok {
		s.log.Log(LogEntry{Time: at, Op: op, Output: "rejected", Reason: "incident not found"})
		return ErrIncidentNotFound
	}
	if in.ended {
		s.log.Log(LogEntry{Time: at, Op: op, Output: "rejected", Reason: "incident already ended"})
		return ErrIncidentAlreadyEnded
	}
	if at < in.start-eps {
		s.log.Log(LogEntry{Time: at, Op: op, Output: "rejected", Reason: "before incident start"})
		return ErrInvalidArgument
	}
	if reduction != nil && (*reduction < 0-eps || *reduction > 1+eps) {
		s.log.Log(LogEntry{Time: at, Op: op, Output: "rejected", Reason: "reduction out of range"})
		return ErrReductionOutOfRange
	}
	s.advanceTo(at)
	reason := run(in)
	s.syncRates()
	s.nextEq = s.computeEquilibrium()
	s.resetRates()
	s.log.Log(LogEntry{Time: at, Op: op, Output: "accepted",
		Input: "incident=" + itoa(incidentID) + " at=" + ftoa(at), Reason: reason})
	return nil
}

func (s *Service) insertPending(in *incident) {
	pos := len(s.pending)
	for i, id := range s.pending {
		if s.inc[id].start > in.start+eps {
			pos = i
			break
		}
	}
	s.pending = append(s.pending, 0)
	copy(s.pending[pos+1:], s.pending[pos:])
	s.pending[pos] = in.id
}

func (s *Service) removePending(id int) {
	for i, pid := range s.pending {
		if pid == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func mathInf(sign int) float64 { return math.Inf(sign) }

func ftoa(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func itoa(v int) string { return strconv.Itoa(v) }

func incidentInput(id, link int, start, reduction float64) string {
	return "incident=" + itoa(id) + " link=" + itoa(link) +
		" start=" + ftoa(start) + " reduction=" + ftoa(reduction)
}

// advanceTo 把内部时钟推进到 t，依次处理所有不晚于 t 的内部结构事件。
// 切分为任意次数的调用都得到同一结果：状态完全由分段线性锚点决定。
func (s *Service) advanceTo(t float64) {
	for {
		// 若下一个待开始事件落在 (now, t] 内，先精确跳到该时刻再激活。
		if len(s.pending) > 0 {
			in := s.inc[s.pending[0]]
			if in.start > s.now+eps && in.start <= t+eps {
				s.jumpTo(in.start)
			}
		}
		// 激活开始时刻已到的待开始事件。
		for len(s.pending) > 0 {
			id := s.pending[0]
			in := s.inc[id]
			if in.start > s.now+eps {
				break
			}
			s.activate(in)
			s.pending = s.pending[1:]
		}

		next := s.nextBoundary(t)
		if next > t+eps {
			break
		}
		s.stepTo(next)
	}
	s.now = t
}

// jumpTo 在不跨越结构事件的前提下把锚点平移到 t（专供到达未来事件开始点）。
func (s *Service) jumpTo(t float64) {
	for i := range s.states {
		st := &s.states[i]
		st.q = st.queueAt(t)
		st.anchorTime = t
	}
	s.now = t
}

// activate 在事件开始时刻把它纳入生效集合并重算均衡与速率。
func (s *Service) activate(in *incident) {
	s.syncRates()
	s.nextEq = s.computeEquilibrium()
	s.resetRates()
}

// nextBoundary 求不早于 now、不晚于 limit 的最早排队结构事件时刻。
func (s *Service) nextBoundary(limit float64) float64 {
	best := mathInf(1)
	for i := range s.states {
		st := &s.states[i]
		rate := s.links[i].Arrival - s.nextEq.cap[i]
		if st.full {
			if rate < -eps {
				hit := s.now + (0-st.queueAt(s.now))/rate
				if hit >= s.now-eps && hit <= limit+eps && hit < best {
					best = hit
				}
			}
			continue
		}
		q := st.queueAt(s.now)
		if rate > eps && q < s.capVeh[i]-eps {
			hit := s.now + (s.capVeh[i]-q)/rate
			if hit <= limit+eps && hit < best {
				best = hit
			}
		} else if rate < -eps && q > eps {
			hit := s.now + (0-q)/rate
			if hit <= limit+eps && hit < best {
				best = hit
			}
		}
	}
	return best
}

// stepTo 推进到单个边界时刻并翻转恰好到达 0 或容量的路段。
func (s *Service) stepTo(t float64) {
	for i := range s.states {
		st := &s.states[i]
		q := st.queueAt(t)
		if q < 0 {
			q = 0
		}
		if q > s.capVeh[i] {
			q = s.capVeh[i]
		}
		st.anchorTime, st.q = t, q
	}
	for i := range s.states {
		st := &s.states[i]
		if st.full && st.q <= eps {
			st.full = false
			st.q = 0
		} else if !st.full && st.q >= s.capVeh[i]-eps {
			st.full = true
			st.q = s.capVeh[i]
		}
	}
	s.nextEq = s.computeEquilibrium()
	for i := range s.states {
		s.states[i].rate = s.links[i].Arrival - s.nextEq.cap[i]
	}
	s.now = t
}

// syncRates 在 now 处固化全部锚点并按当前均衡重设速率。
func (s *Service) syncRates() {
	for i := range s.states {
		st := &s.states[i]
		q := st.queueAt(s.now)
		if q < 0 {
			q = 0
		}
		if st.full && q >= s.capVeh[i]-eps {
			q = s.capVeh[i]
		}
		st.anchorTime, st.q = s.now, q
	}
	s.resetRates()
}

// resetRates 按当前 nextEq 重设各路段演化速率。
func (s *Service) resetRates() {
	for i := range s.states {
		s.states[i].rate = s.links[i].Arrival - s.nextEq.cap[i]
	}
}
