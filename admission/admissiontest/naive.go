package admissiontest

import (
	"fmt"
	"sort"

	"ontology/admission"
)

type naiveLevel struct {
	kind       admission.LevelKind
	share      int64
	queueLimit int64
	timeout    int64
}

type NaiveConfig struct {
	totalSeats int64
	rules      []admission.Rule
	levels     map[string]naiveLevel
	limited    []string
	nominal    map[string]int64
}

type naiveWaiter struct {
	id       string
	seats    int64
	level    string
	flow     string
	deadline admission.Time
}

type naiveLive struct {
	id       string
	level    string
	flow     string
	seats    int64
	limited  bool
	executed bool
}

type NaiveEvent struct {
	Kind     string
	ID       string
	Level    string
	Flow     string
	Seats    int64
	Deadline admission.Time
}

type NaiveResult struct {
	Decision admission.Decision
	Level    string
	Flow     string
	ErrClass admission.ErrorClass
	Events   []NaiveEvent
}

type NaiveModel struct {
	now           admission.Time
	cfg           NaiveConfig
	live          map[string]naiveLive
	used          map[string]int64
	queues        map[string][]*naiveWaiter
	activation    map[string]map[string]int64
	activationSeq map[string]int64
	lastServed    map[string]string
	cursor        map[string]string
	blocked       map[string]bool
	served        map[string]map[string]bool
}

func BuildNaiveConfig(cfg *admission.Config) (NaiveConfig, error) {
	out := NaiveConfig{
		totalSeats: cfg.TotalSeats,
		rules:      append([]admission.Rule(nil), cfg.Rules...),
		levels:     map[string]naiveLevel{},
		nominal:    map[string]int64{},
	}
	if cfg == nil || cfg.TotalSeats < 0 {
		return out, fmt.Errorf("invalid config")
	}
	sort.SliceStable(out.rules, func(i, j int) bool {
		if out.rules[i].Priority != out.rules[j].Priority {
			return out.rules[i].Priority < out.rules[j].Priority
		}
		return out.rules[i].Name < out.rules[j].Name
	})
	seenLevel := map[string]bool{}
	for _, lv := range cfg.Levels {
		if lv.Name == "" || seenLevel[lv.Name] {
			return out, fmt.Errorf("invalid level")
		}
		seenLevel[lv.Name] = true
		switch lv.Kind {
		case admission.LevelExempt:
			out.levels[lv.Name] = naiveLevel{kind: admission.LevelExempt}
		case admission.LevelLimited:
			if lv.Share <= 0 || lv.QueueLimit < 0 || lv.Timeout <= 0 {
				return out, fmt.Errorf("invalid limited level")
			}
			out.levels[lv.Name] = naiveLevel{
				kind: admission.LevelLimited, share: lv.Share,
				queueLimit: lv.QueueLimit, timeout: lv.Timeout,
			}
			out.limited = append(out.limited, lv.Name)
		default:
			return out, fmt.Errorf("invalid level kind")
		}
	}
	seenRule := map[string]bool{}
	for _, r := range out.rules {
		if r.Name == "" || seenRule[r.Name] {
			return out, fmt.Errorf("invalid rule")
		}
		seenRule[r.Name] = true
		if _, ok := out.levels[r.TargetLevel]; !ok {
			return out, fmt.Errorf("unknown target level")
		}
		if r.Distinguish != admission.FlowByUser && r.Distinguish != admission.FlowByNamespace {
			return out, fmt.Errorf("invalid distinguish")
		}
	}
	out.nominal = allocateNaiveSeats(cfg.TotalSeats, out)
	sort.Strings(out.limited)
	return out, nil
}

func allocateNaiveSeats(total int64, cfg NaiveConfig) map[string]int64 {
	nominal := map[string]int64{}
	if len(cfg.limited) == 0 {
		return nominal
	}
	type pair struct {
		name  string
		share int64
	}
	var arr []pair
	var sum int64
	for _, name := range cfg.limited {
		sh := cfg.levels[name].share
		arr = append(arr, pair{name, sh})
		sum += sh
	}
	var given int64
	for _, p := range arr {
		n := total * p.share / sum
		nominal[p.name] = n
		given += n
	}
	remaining := total - given
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].share != arr[j].share {
			return arr[i].share > arr[j].share
		}
		return arr[i].name < arr[j].name
	})
	for remaining > 0 {
		for _, p := range arr {
			if remaining == 0 {
				break
			}
			nominal[p.name]++
			remaining--
		}
	}
	return nominal
}

func NewNaiveModel(cfg *admission.Config) (*NaiveModel, error) {
	compiled, err := BuildNaiveConfig(cfg)
	if err != nil {
		return nil, err
	}
	m := &NaiveModel{
		cfg:           compiled,
		live:          map[string]naiveLive{},
		used:          map[string]int64{},
		queues:        map[string][]*naiveWaiter{},
		activation:    map[string]map[string]int64{},
		activationSeq: map[string]int64{},
		lastServed:    map[string]string{},
		cursor:        map[string]string{},
		blocked:       map[string]bool{},
		served:        map[string]map[string]bool{},
	}
	for _, name := range compiled.limited {
		m.queues[name] = nil
		m.activation[name] = map[string]int64{}
		m.lastServed[name] = ""
		m.cursor[name] = ""
		m.blocked[name] = false
		m.served[name] = map[string]bool{}
	}
	return m, nil
}

func (m *NaiveModel) validate(req *admission.Request) admission.ErrorClass {
	if req == nil || req.ID == "" || req.User == "" || req.Namespace == "" {
		return admission.ClassInvalidArgument
	}
	if req.Seats < 1 || req.Seats > 10 {
		return admission.ClassInvalidArgument
	}
	return 0
}

func contains(set []string, v string) bool {
	if len(set) == 0 {
		return true
	}
	for _, x := range set {
		if x == v {
			return true
		}
	}
	return false
}

func (m *NaiveModel) classify(req *admission.Request) *admission.Rule {
	for i := range m.cfg.rules {
		r := &m.cfg.rules[i]
		if contains(r.Match.UserGroups, req.UserGroup) &&
			contains(r.Match.Verbs, req.Verb) &&
			contains(r.Match.Resources, req.Resource) {
			return r
		}
	}
	return nil
}

func flowOf(r *admission.Rule, req *admission.Request) string {
	if r.Distinguish == admission.FlowByNamespace {
		return "ns:" + req.Namespace
	}
	return "user:" + req.User
}

func (m *NaiveModel) flowExists(level, flow string) bool {
	for _, w := range m.queues[level] {
		if w.flow == flow {
			return true
		}
	}
	return false
}

func (m *NaiveModel) activate(level, flow string) {
	if m.flowExists(level, flow) {
		return
	}
	m.activationSeq[level]++
	m.activation[level][flow] = m.activationSeq[level]
}

func (m *NaiveModel) expireLevel(level string, now admission.Time, events *[]NaiveEvent) {
	last := m.lastServed[level]
	var kept []*naiveWaiter
	for _, w := range m.queues[level] {
		if w.deadline <= now {
			delete(m.live, w.id)
			*events = append(*events, NaiveEvent{
				Kind: "timeout-rejected", ID: w.id, Level: level,
				Flow: w.flow, Seats: w.seats, Deadline: w.deadline,
			})
			if w.flow == last {
				last = ""
			}
			continue
		}
		kept = append(kept, w)
	}
	m.queues[level] = kept
	m.reapEmptyFlows(level)
	if last == "" {
		m.lastServed[level] = ""
	}
	m.invalidateSchedule(level)
}

func (m *NaiveModel) orderedFlows(level string) []string {
	type pair struct {
		flow string
		seq  int64
	}
	seen := map[string]bool{}
	var arr []pair
	for _, w := range m.queues[level] {
		if !seen[w.flow] {
			seen[w.flow] = true
			arr = append(arr, pair{w.flow, m.activation[level][w.flow]})
		}
	}
	sort.SliceStable(arr, func(i, j int) bool { return arr[i].seq < arr[j].seq })
	out := make([]string, 0, len(arr))
	for _, p := range arr {
		out = append(out, p.flow)
	}
	return out
}

func (m *NaiveModel) nextFlowAfter(level, flow string) string {
	flows := m.orderedFlows(level)
	for i, f := range flows {
		if f == flow && i+1 < len(flows) {
			return flows[i+1]
		}
	}
	return ""
}

func (m *NaiveModel) beginPump(level string) {
	flows := m.orderedFlows(level)
	if len(flows) == 0 {
		m.cursor[level] = ""
		m.blocked[level] = false
		return
	}
	if !m.blocked[level] {
		for f := range m.served[level] {
			m.served[level][f] = false
		}
	}
	if m.cursor[level] == "" {
		m.cursor[level] = flows[0]
	}
	if _, ok := m.activation[level][m.cursor[level]]; !ok {
		m.cursor[level] = flows[0]
	}
}

func (m *NaiveModel) nextPumpFlow(level string) (string, bool) {
	flows := m.orderedFlows(level)
	if len(flows) == 0 {
		return "", false
	}
	start := m.cursor[level]
	if start == "" {
		start = flows[0]
	}
	startIdx := 0
	for i, f := range flows {
		if f == start {
			startIdx = i
			break
		}
	}
	for pass := 0; pass < 2; pass++ {
		for i := startIdx; i < len(flows); i++ {
			flow := flows[i]
			if !m.served[level][flow] {
				return flow, true
			}
		}
		for f := range m.served[level] {
			m.served[level][f] = false
		}
		startIdx = 0
	}
	return "", false
}

func (m *NaiveModel) invalidateSchedule(level string) {
	flows := m.orderedFlows(level)
	if len(flows) > 0 {
		m.lastServed[level] = ""
	} else {
		m.lastServed[level] = ""
	}
	m.blocked[level] = false
}

func (m *NaiveModel) flowHead(level, flow string) *naiveWaiter {
	for _, w := range m.queues[level] {
		if w.flow == flow {
			return w
		}
	}
	return nil
}

func (m *NaiveModel) remove(level, id string) {
	var kept []*naiveWaiter
	for _, w := range m.queues[level] {
		if w.id != id {
			kept = append(kept, w)
		}
	}
	m.queues[level] = kept
	m.reapEmptyFlows(level)
}

// reapEmptyFlows 正常出队后，流变空即删除其最近激活时间；
// 它再次有等待者时会以新的“空→非空”时间排到轮转尾部。
func (m *NaiveModel) reapEmptyFlows(level string) {
	exists := map[string]bool{}
	for _, w := range m.queues[level] {
		exists[w.flow] = true
	}
	for flow := range m.activation[level] {
		if !exists[flow] {
			delete(m.activation[level], flow)
			delete(m.served[level], flow)
			if m.cursor[level] == flow {
				m.cursor[level] = m.nextFlowAfter(level, flow)
			}
			if m.blocked[level] {
				m.cursor[level] = ""
			}
		}
	}
	if len(m.queues[level]) == 0 {
		m.cursor[level] = ""
		m.blocked[level] = false
	}
}

// pumpLevel 用跨操作游标进行轮转。
func (m *NaiveModel) pumpLevel(level string, events *[]NaiveEvent) {
	for {
		flows := m.orderedFlows(level)
		progress := false
		for _, flow := range flows {
			w := m.flowHead(level, flow)
			if w == nil {
				continue
			}
			if m.used[level]+w.seats > m.cfg.nominal[level] {
				m.blocked[level] = true
				m.cursor[level] = flow
				return
			}
			m.remove(level, w.id)
			m.used[level] += w.seats
			lr := m.live[w.id]
			lr.executed = true
			m.live[w.id] = lr
			m.blocked[level] = false
			if m.flowExists(level, flow) {
				m.activationSeq[level]++
				m.activation[level][flow] = m.activationSeq[level]
			}
			m.lastServed[level] = flow
			*events = append(*events, NaiveEvent{
				Kind: "executed", ID: w.id, Level: level,
				Flow: w.flow, Seats: w.seats, Deadline: w.deadline,
			})
			progress = true
		}
		if !progress {
			return
		}
	}
}

func (m *NaiveModel) pumpAll(events *[]NaiveEvent) {
	for _, level := range m.cfg.limited {
		m.pumpLevel(level, events)
	}
}

func (m *NaiveModel) advance(now admission.Time, events *[]NaiveEvent) {
	for _, level := range m.cfg.limited {
		m.expireLevel(level, now, events)
	}
}

func (m *NaiveModel) Submit(req *admission.Request, now admission.Time) NaiveResult {
	if class := m.validate(req); class != 0 {
		return NaiveResult{ErrClass: class}
	}
	if _, dup := m.live[req.ID]; dup {
		return NaiveResult{ErrClass: admission.ClassInvalidArgument}
	}
	if now < m.now {
		return NaiveResult{ErrClass: admission.ClassClockRewind}
	}
	m.now = now
	var events []NaiveEvent
	m.advance(now, &events)

	rule := m.classify(req)
	if rule == nil {
		return NaiveResult{ErrClass: admission.ClassNoMatch, Events: events}
	}
	flow := flowOf(rule, req)
	level := rule.TargetLevel
	if m.cfg.levels[level].kind == admission.LevelExempt {
		m.live[req.ID] = naiveLive{id: req.ID, level: level, flow: flow, executed: true}
		return NaiveResult{
			Decision: admission.DecisionExecuted, Level: level,
			Flow: flow, Events: events,
		}
	}

	if m.cfg.nominal[level] == 0 || req.Seats > m.cfg.nominal[level] {
		return NaiveResult{
			Decision: admission.DecisionRejected, Level: level, Flow: flow,
			ErrClass: admission.ClassInsufficientSeat, Events: events,
		}
	}
	if m.used[level]+req.Seats <= m.cfg.nominal[level] && len(m.queues[level]) == 0 {
		m.used[level] += req.Seats
		m.live[req.ID] = naiveLive{
			id: req.ID, level: level, flow: flow, seats: req.Seats,
			limited: true, executed: true,
		}
		return NaiveResult{
			Decision: admission.DecisionExecuted, Level: level,
			Flow: flow, Events: events,
		}
	}
	if int64(len(m.queues[level])) >= m.cfg.levels[level].queueLimit {
		return NaiveResult{
			Decision: admission.DecisionRejected, Level: level, Flow: flow,
			ErrClass: admission.ClassQueueFull, Events: events,
		}
	}
	w := &naiveWaiter{
		id:       req.ID,
		seats:    req.Seats,
		level:    level,
		flow:     flow,
		deadline: now + admission.Time(m.cfg.levels[level].timeout),
	}
	m.activate(level, flow)
	m.queues[level] = append(m.queues[level], w)
	m.live[req.ID] = naiveLive{
		id: req.ID, level: level, flow: flow, seats: req.Seats, limited: true,
	}
	return NaiveResult{
		Decision: admission.DecisionQueued, Level: level,
		Flow: flow, Events: events,
	}
}

func (m *NaiveModel) Complete(id string, now admission.Time) (NaiveResult, admission.ErrorClass) {
	if now < m.now {
		return NaiveResult{ErrClass: admission.ClassClockRewind}, admission.ClassClockRewind
	}
	var events []NaiveEvent
	lr, ok := m.live[id]
	if ok && lr.executed {
		m.now = now
		m.advance(now, &events)
		delete(m.live, id)
		if lr.limited {
			m.used[lr.level] -= lr.seats
			m.pumpLevel(lr.level, &events)
		}
		return NaiveResult{Events: events}, 0
	}
	m.now = now
	m.advance(now, &events)
	return NaiveResult{Events: events}, 0
}

func (m *NaiveModel) UpdateConfig(cfg *admission.Config, now admission.Time) (NaiveResult, error) {
	if now < m.now {
		return NaiveResult{}, fmt.Errorf("clock rewind")
	}
	compiled, err := BuildNaiveConfig(cfg)
	if err != nil {
		return NaiveResult{}, err
	}
	compiled.nominal = allocateNaiveSeats(cfg.TotalSeats, compiled)

	// 语义合法性：在途/排队者引用的级别仍以同种类别存在；在途占用不超过新名义席位。
	anyRunningSeats := int64(0)
	for _, lr := range m.live {
		if lr.executed && lr.limited && lr.seats > anyRunningSeats {
			anyRunningSeats = lr.seats
		}
	}
	for _, lr := range m.live {
		newLevel, ok := compiled.levels[lr.level]
		if !ok {
			return NaiveResult{}, fmt.Errorf("level missing")
		}
		if !lr.limited {
			if newLevel.kind != admission.LevelExempt {
				return NaiveResult{}, fmt.Errorf("exempt level changed kind")
			}
			continue
		}
		if newLevel.kind != admission.LevelLimited {
			return NaiveResult{}, fmt.Errorf("limited level changed kind")
		}
		if lr.executed && compiled.nominal[lr.level] < anyRunningSeats {
			return NaiveResult{}, fmt.Errorf("nominal below running request")
		}
	}

	var events []NaiveEvent
	m.now = now
	m.cfg = compiled
	// 保证内部 map 包含新配置中的级别。
	for _, name := range compiled.limited {
		if _, ok := m.queues[name]; !ok {
			m.queues[name] = nil
			m.activation[name] = map[string]int64{}
			m.lastServed[name] = ""
			m.cursor[name] = ""
			m.blocked[name] = false
			m.served[name] = map[string]bool{}
		}
	}
	m.advance(now, &events)

	// 新名义席位使排队请求永远无法满足：更新时拒绝。
	for _, level := range compiled.limited {
		m.blocked[level] = false
		m.invalidateSchedule(level)
	}
	// 热更新拒绝不是常态出队路径，按全局入队 FIFO 输出。
	for _, level := range compiled.limited {
		var kept []*naiveWaiter
		for _, w := range m.queues[level] {
			if w.seats > compiled.nominal[level] {
				delete(m.live, w.id)
				events = append(events, NaiveEvent{
					Kind: "update-rejected", ID: w.id, Level: level,
					Flow: w.flow, Seats: w.seats,
				})
				continue
			}
			kept = append(kept, w)
		}
		m.queues[level] = kept
		m.reapEmptyFlows(level)
		m.invalidateSchedule(level)
	}
	m.pumpAll(&events)
	return NaiveResult{Events: events}, nil
}

func (m *NaiveModel) Snapshot() map[string]int64 {
	out := map[string]int64{}
	for level, used := range m.used {
		out[level] = used
	}
	return out
}
