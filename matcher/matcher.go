package matcher

import (
	"context"
	"fmt"
	"sort"

	"ontology/graph"
)

// Match 是一次成功的匹配：变量名 -> 对象 ID。
type Match map[string]string

// Stats 记录搜索过程中的判定依据，用于剪枝效果统计与日志。
type Stats struct {
	CandidateCount       map[string]int
	Backtracks           int
	PrunedByConstraint   int
	PrunedByEdge         int
	IsomorphicDuplicates int
	Embeddings           int
}

// Logger 是匹配器使用的日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

// Matcher 在对象图上执行模式匹配。零值不可用，请用 New 构造。
type Matcher struct {
	log Logger
	// 下列钩子默认采用正确实现；同包测试会替换它们，以验证三类
	// “必须拒绝且不得返回部分结果”的不变量在真实搜索路径上被强制执行。
	checkBinding func(varName string, assignment map[string]string, id string) error
	onDuplicate  func(emb map[string]string) error
	enumerate    func(snap graph.Snapshot, node NodeVar) ([]string, error)
}

// Option 配置 Matcher。
type Option func(*Matcher)

// WithLogger 注入日志记录器；日志打印模式、每个匹配与判定依据。
func WithLogger(l Logger) Option {
	return func(m *Matcher) { m.log = l }
}

// New 创建匹配器。
func New(opts ...Option) *Matcher {
	m := &Matcher{
		checkBinding: defaultCheckBinding,
		onDuplicate:  defaultOnDuplicate,
		enumerate:    defaultEnumerate,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func defaultCheckBinding(varName string, assignment map[string]string, id string) error {
	if existing, ok := assignment[varName]; ok && existing != id {
		return fmt.Errorf("%w: variable %q bound to both %q and %q",
			ErrInconsistentBinding, varName, existing, id)
	}
	return nil
}

// defaultOnDuplicate 抑制同构重复嵌入（去重）。
func defaultOnDuplicate(map[string]string) error { return nil }

// defaultEnumerate 给出变量的初始候选：有类型时只枚举该类型对象；
// 无类型时必须通过边邻接获得候选，直接枚举全图被视为全搜索退化并拒绝。
func defaultEnumerate(snap graph.Snapshot, node NodeVar) ([]string, error) {
	if node.Type == "" {
		return nil, fmt.Errorf("%w: variable %q has no type constraint",
			ErrFullScanDegeneration, node.Name)
	}
	return snap.ObjectsByType(node.Type), nil
}

// incidentEdge 描述一条与某变量相邻的模式边及其相对方向。
type incidentEdge struct {
	edgeType string
	other    string
	outgoing bool
}

type searchState struct {
	pattern  *Pattern
	snap     graph.Snapshot
	matcher  *Matcher
	domains  map[string][]string
	incident map[string][]incidentEdge
	assign   map[string]string
	used     map[string]bool
	order    []string
	seenKey  map[string]struct{}
	matches  []Match
	stats    Stats
}

// Match 在给定快照上匹配模式，返回去重后的全部匹配（顺序确定）。
// 模式非法或搜索被拒绝时返回可区分原因的错误，且不返回任何部分结果。
func (m *Matcher) Match(ctx context.Context, snap graph.Snapshot, p *Pattern) ([]Match, Stats, error) {
	if err := p.Validate(); err != nil {
		m.logPatternRejected(p, err)
		return nil, Stats{}, err
	}

	state, err := m.prepare(ctx, snap, p)
	if err != nil {
		m.logPatternRejected(p, err)
		// 被拒绝的查询不返回部分结果。
		return nil, Stats{CandidateCount: candidateSizes(p)}, err
	}
	m.logPatternStart(p, state)

	if len(state.order) > 0 {
		if err := state.search(ctx, 0); err != nil {
			m.logPatternRejected(p, err)
			return nil, Stats{}, err
		}
	}

	state.matches = sortMatches(state.matches, p)
	m.logSummary(p, state)
	return state.matches, state.stats, nil
}

func candidateSizes(p *Pattern) map[string]int {
	sizes := make(map[string]int, len(p.Nodes))
	for _, node := range p.Nodes {
		sizes[node.Name] = 0
	}
	return sizes
}

func (m *Matcher) prepare(ctx context.Context, snap graph.Snapshot, p *Pattern) (*searchState, error) {
	domains := make(map[string][]string, len(p.Nodes))
	state := &searchState{
		pattern:  p,
		snap:     snap,
		matcher:  m,
		domains:  domains,
		incident: make(map[string][]incidentEdge, len(p.Nodes)),
		assign:   make(map[string]string, len(p.Nodes)),
		used:     make(map[string]bool, len(p.Nodes)),
		seenKey:  make(map[string]struct{}),
		stats:    Stats{CandidateCount: make(map[string]int, len(p.Nodes))},
	}

	// 初始候选：按类型取对象，再用属性约束立即过滤（尽早剪枝）。
	for _, node := range p.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates, err := m.enumerate(snap, node)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]bool, len(candidates))
		filtered := make([]string, 0, len(candidates))
		for _, id := range candidates {
			if seen[id] {
				continue
			}
			seen[id] = true
			obj, ok := snap.Object(id)
			if !ok {
				continue
			}
			if satisfiesAll(obj, node.Constraints) {
				filtered = append(filtered, id)
			} else {
				state.stats.PrunedByConstraint++
			}
		}
		sort.Strings(filtered)
		domains[node.Name] = filtered
		state.stats.CandidateCount[node.Name] = len(filtered)
	}

	for _, edge := range p.Edges {
		state.incident[edge.Source] = append(state.incident[edge.Source],
			incidentEdge{edgeType: edge.Type, other: edge.Target, outgoing: true})
		state.incident[edge.Target] = append(state.incident[edge.Target],
			incidentEdge{edgeType: edge.Type, other: edge.Source, outgoing: false})
	}

	// 任意变量初始候选为空，模式不可能匹配；直接剪枝，不进入搜索。
	for _, node := range p.Nodes {
		if len(domains[node.Name]) == 0 {
			return state, nil
		}
	}
	state.order = chooseOrder(p, domains)
	return state, nil
}

// chooseOrder 用 MRV（最少候选优先）+ 连通优先选择变量顺序，
// 使回溯搜索尽早失败、尽早剪枝。
func chooseOrder(p *Pattern, domains map[string][]string) []string {
	adjacent := make(map[string]map[string]bool, len(p.Nodes))
	for _, edge := range p.Edges {
		if adjacent[edge.Source] == nil {
			adjacent[edge.Source] = map[string]bool{}
		}
		if adjacent[edge.Target] == nil {
			adjacent[edge.Target] = map[string]bool{}
		}
		adjacent[edge.Source][edge.Target] = true
		adjacent[edge.Target][edge.Source] = true
	}

	remaining := make(map[string]bool, len(p.Nodes))
	for _, node := range p.Nodes {
		remaining[node.Name] = true
	}
	order := make([]string, 0, len(p.Nodes))
	for len(remaining) > 0 {
		var picked string
		bestSize := -1
		connected := false
		for name := range remaining {
			size := len(domains[name])
			touches := false
			for prior := range adjacent[name] {
				if !remaining[prior] {
					touches = true
					break
				}
			}
			better := false
			switch {
			case picked == "":
				better = true
			case touches != connected:
				better = touches
			case size != bestSize:
				better = size < bestSize
			default:
				better = name < picked
			}
			if better {
				picked, bestSize, connected = name, size, touches
			}
		}
		delete(remaining, picked)
		order = append(order, picked)
	}
	return order
}

func (s *searchState) search(ctx context.Context, depth int) error {
	if depth == len(s.order) {
		return s.recordEmbedding(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name := s.order[depth]
	for _, id := range s.domains[name] {
		// 注入性：不同变量必须绑定不同对象。
		if s.used[id] {
			s.stats.PrunedByEdge++
			continue
		}
		// 同变量一致：同一变量必须始终绑定同一对象。
		if err := s.matcher.checkBinding(name, s.assign, id); err != nil {
			return err
		}
		// 与所有已绑定邻接变量做边一致性检查（尽早剪枝）。
		if !s.edgeConsistent(name, id) {
			s.stats.Backtracks++
			s.stats.PrunedByEdge++
			continue
		}
		// 前向检查：未绑定邻接变量必须仍存在可行候选。
		if !s.forwardCheck(name, id) {
			s.stats.Backtracks++
			s.stats.PrunedByEdge++
			continue
		}
		s.assign[name] = id
		s.used[id] = true
		if err := s.search(ctx, depth+1); err != nil {
			delete(s.assign, name)
			delete(s.used, id)
			return err
		}
		delete(s.assign, name)
		delete(s.used, id)
	}
	return nil
}

func (s *searchState) edgeConsistent(name, id string) bool {
	for _, inc := range s.incident[name] {
		otherID, bound := s.assign[inc.other]
		if !bound {
			continue
		}
		if inc.outgoing {
			if !containsString(s.snap.OutNeighbors(id, inc.edgeType), otherID) {
				return false
			}
		} else {
			if !containsString(s.snap.InNeighbors(id, inc.edgeType), otherID) {
				return false
			}
		}
	}
	return true
}

func (s *searchState) forwardCheck(name, id string) bool {
	for _, inc := range s.incident[name] {
		if _, bound := s.assign[inc.other]; bound {
			continue
		}
		var neighbors []string
		if inc.outgoing {
			neighbors = s.snap.OutNeighbors(id, inc.edgeType)
		} else {
			neighbors = s.snap.InNeighbors(id, inc.edgeType)
		}
		feasible := false
		for _, candidate := range s.domains[inc.other] {
			if containsString(neighbors, candidate) && !s.used[candidate] {
				feasible = true
				break
			}
		}
		if !feasible {
			return false
		}
	}
	return true
}

func (s *searchState) recordEmbedding(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.stats.Embeddings++
	emb := make(map[string]string, len(s.assign))
	for name, id := range s.assign {
		emb[name] = id
	}
	// 结果二次校验：拒绝任何不满足完整模式的“匹配”，
	// 保证被接受的结果一定经过全部节点类型、属性与边约束判定。
	if !verifyEmbedding(s.snap, s.pattern, emb) {
		return fmt.Errorf("%w: candidate embedding failed full pattern verification",
			ErrFullScanDegeneration)
	}
	key := canonicalKey(s.pattern, emb)
	if _, dup := s.seenKey[key]; dup {
		s.stats.IsomorphicDuplicates++
		s.matcher.logDuplicate(s.pattern, emb, key)
		// 去重钩子默认抑制重复；若重复仍被要求输出则返回
		// ErrDuplicateMatch，整个查询被拒绝且不返回部分结果。
		if err := s.matcher.onDuplicate(emb); err != nil {
			return err
		}
		return nil
	}
	s.seenKey[key] = struct{}{}
	s.matches = append(s.matches, emb)
	s.matcher.logMatch(s.pattern, emb, key)
	return nil
}

func satisfiesAll(o *graph.Object, constraints []Constraint) bool {
	for _, constraint := range constraints {
		if !constraint.matches(o) {
			return false
		}
	}
	return true
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func orderedNames(p *Pattern) []string {
	names := make([]string, 0, len(p.Nodes))
	for _, node := range p.Nodes {
		names = append(names, node.Name)
	}
	sort.Strings(names)
	return names
}

// verifyEmbedding 对完整嵌入做最终判定：所有变量有绑定、注入、
// 节点类型与属性约束、全部模式边均存在。
func verifyEmbedding(snap graph.Snapshot, p *Pattern, emb map[string]string) bool {
	bound := make(map[string]bool, len(p.Nodes))
	for _, node := range p.Nodes {
		id, ok := emb[node.Name]
		if !ok {
			return false
		}
		obj, ok := snap.Object(id)
		if !ok {
			return false
		}
		if node.Type != "" && obj.Type != node.Type {
			return false
		}
		if !satisfiesAll(obj, node.Constraints) {
			return false
		}
		if bound[id] {
			return false
		}
		bound[id] = true
	}
	for _, edge := range p.Edges {
		if !containsString(snap.OutNeighbors(emb[edge.Source], edge.Type), emb[edge.Target]) {
			return false
		}
	}
	return true
}

// sortMatches 以变量名排序后的对象 ID 序列作为匹配的确定顺序，
// 与边插入顺序、搜索路径无关。
func sortMatches(matches []Match, p *Pattern) []Match {
	names := orderedNames(p)
	sort.Slice(matches, func(i, j int) bool {
		for _, name := range names {
			if matches[i][name] != matches[j][name] {
				return matches[i][name] < matches[j][name]
			}
		}
		return false
	})
	return matches
}
