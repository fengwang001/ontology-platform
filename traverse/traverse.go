// Package traverse 实现支持深度上限与结果条数上限双维度独立限制的图遍历服务。
//
// 语义定义（确定性、可复现）：
//
//  1. 分支展开次序：自起始对象做深度优先展开；每个对象的可走链接按
//     (方向, 链接类型, 对端对象 ID, 链接 ID) 字典序全序排列。
//     同一输入与上限组合在任意次执行下得到完全相同的结果与标记分布。
//  2. 深度上限 D：路径扩展到第 D 跳即停止向下扩展（深度上限对该路径触发）；
//     若路径满足返回条件（跳数 >= 1）仍计入结果。
//  3. 结果条数上限 N：已收集路径数达到 N 后，停止扩展一切尚未扩展与
//     扩展到一半的分支（一视同仁，无优先完成）。最终返回路径数 <= N。
//  4. 截断标记（两两互斥）：
//     - depth_only：路径在第 D 跳被深度上限截断，且计入结果时条数上限未饱和。
//     - depth_then_limit：路径先在第 D 跳被深度上限截断，计入结果时恰好使
//     已收集数达到 N（条数上限随后在该分支上触发）。该路径仍被返回。
//     - limit_only：分支因条数上限饱和被停止扩展，且其未扩展部分在剩余
//     深度预算内本就无法触及深度上限。
//     - limit_then_depth：分支因条数上限饱和被停止扩展，但其未扩展部分
//     在剩余深度预算内本可触及深度上限（条数先触发，深度本也会触发）。
package traverse

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"ontology/graph"
)

// Marker 标记一条分支的截断原因，四种截断情形两两互斥。
type Marker string

const (
	// MarkerNone 表示分支完整返回，未被任何上限截断。
	MarkerNone Marker = "none"
	// MarkerDepthOnly 表示分支仅因深度上限被截断。
	MarkerDepthOnly Marker = "depth_only"
	// MarkerLimitOnly 表示分支仅因结果条数上限被截断。
	MarkerLimitOnly Marker = "limit_only"
	// MarkerDepthThenLimit 表示同一分支上深度上限先于结果条数上限触发。
	MarkerDepthThenLimit Marker = "depth_then_limit"
	// MarkerLimitThenDepth 表示同一分支上结果条数上限先于深度上限触发。
	MarkerLimitThenDepth Marker = "limit_then_depth"
)

// Step 是路径上的一跳。
type Step struct {
	Dir      graph.Direction
	LinkID   string
	LinkType string
	FromID   string
	ToID     string
}

// Path 是从起始对象出发的一条路径（允许经过重复对象的游走）。
type Path struct {
	StartID string
	Steps   []Step
}

// Depth 返回路径已扩展的跳数。
func (p Path) Depth() int { return len(p.Steps) }

// EndID 返回路径末端对象 ID。
func (p Path) EndID() string {
	if len(p.Steps) == 0 {
		return p.StartID
	}
	return p.Steps[len(p.Steps)-1].ToID
}

// Extend 返回追加一跳后的新路径。
func (p Path) Extend(s Step) Path {
	steps := make([]Step, len(p.Steps)+1)
	copy(steps, p.Steps)
	steps[len(p.Steps)] = s
	return Path{StartID: p.StartID, Steps: steps}
}

// String 以 "a->b->c" 形式呈现路径经过的对象序列。
func (p Path) String() string {
	var b strings.Builder
	b.WriteString(p.StartID)
	for _, s := range p.Steps {
		fmt.Fprintf(&b, "-[%s:%s:%s]->%s", s.Dir, s.LinkType, s.LinkID, s.ToID)
	}
	return b.String()
}

// Branch 是遍历产出的一条分支记录：已返回的完整路径，或被截断的分支前缀。
type Branch struct {
	Path     Path
	Returned bool
	Marker   Marker
	Reason   string
}

// Stats 记录单次遍历的统计信息，用于验证簿记开销与上限数值无关。
type Stats struct {
	SnapshotVersion uint64
	DepthLimit      int
	ResultLimit     int
	ReturnedCount   int
	TruncatedCount  int
	// BookkeepingOps 记录「已收集路径数是否达到条数上限」这一判定
	// 相关的计数与比较操作次数，与上限数值本身的大小无关。
	BookkeepingOps int64
}

// Result 是一次遍历的完整结果。
type Result struct {
	Returned  []Branch
	Truncated []Branch
	Stats     Stats
}

// Request 是一次遍历的输入。DepthLimit 与 ResultLimit 各自独立配置，
// 互不换算、互不折抵。
type Request struct {
	StartID     string
	Directions  []graph.Direction
	DepthLimit  int
	ResultLimit int
}

// Service 执行图遍历。
type Service struct {
	store  *graph.Store
	logger *slog.Logger
	// OnSnapshot 在快照获取后、遍历开始前触发，仅用于测试。
	OnSnapshot func()
}

// NewService 构造遍历服务；logger 为 nil 时使用默认日志器。
func NewService(store *graph.Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, logger: logger}
}

// Traverse 在遍历开始时的快照上执行一次确定性遍历。
// 输入非法时按固定次序只返回第一类错误，且不产生任何部分结果。
func (s *Service) Traverse(req Request) (*Result, error) {
	snap := s.store.Snapshot()
	if s.OnSnapshot != nil {
		s.OnSnapshot()
	}
	// 错误判定次序固定：起始对象 -> 深度上限 -> 条数上限 -> 方向集合。
	if !snap.HasObject(req.StartID) {
		return nil, ErrStartObjectNotFound
	}
	if req.DepthLimit <= 0 {
		return nil, ErrInvalidDepthLimit
	}
	if req.ResultLimit <= 0 {
		return nil, ErrInvalidResultLimit
	}
	if len(req.Directions) == 0 {
		return nil, ErrEmptyDirections
	}

	dirs := canonicalDirections(req.Directions)
	s.logger.Info("traverse start",
		"start", req.StartID,
		"directions", fmt.Sprint(dirs),
		"depthLimit", req.DepthLimit,
		"resultLimit", req.ResultLimit,
		"snapshotVersion", snap.Version(),
	)

	w := &walker{
		snap: snap,
		dirs: dirs,
		req:  req,
		memo: make(map[reachKey]bool),
	}
	w.visit(Path{StartID: req.StartID})
	sortBranches(w.truncated)

	res := &Result{
		Returned:  w.returned,
		Truncated: w.truncated,
		Stats: Stats{
			SnapshotVersion: snap.Version(),
			DepthLimit:      req.DepthLimit,
			ResultLimit:     req.ResultLimit,
			ReturnedCount:   len(w.returned),
			TruncatedCount:  len(w.truncated),
			BookkeepingOps:  w.ops,
		},
	}
	for _, b := range res.Returned {
		s.logBranch(b)
	}
	for _, b := range res.Truncated {
		s.logBranch(b)
	}
	s.logger.Info("traverse end",
		"returned", res.Stats.ReturnedCount,
		"truncated", res.Stats.TruncatedCount,
		"bookkeepingOps", res.Stats.BookkeepingOps,
	)
	return res, nil
}

func (s *Service) logBranch(b Branch) {
	s.logger.Debug("traverse branch",
		"path", b.Path.String(),
		"returned", b.Returned,
		"marker", string(b.Marker),
		"reason", b.Reason,
	)
}

// canonicalDirections 去重并按字典序排序，保证展开次序确定。
func canonicalDirections(dirs []graph.Direction) []graph.Direction {
	set := make(map[graph.Direction]struct{}, len(dirs))
	out := make([]graph.Direction, 0, len(dirs))
	for _, d := range dirs {
		if _, ok := set[d]; !ok {
			set[d] = struct{}{}
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// walker 携带单次遍历的可变状态。
type walker struct {
	snap *graph.Snapshot
	dirs []graph.Direction
	req  Request

	collected int
	ops       int64
	halted    bool

	returned  []Branch
	truncated []Branch
	memo      map[reachKey]bool
}

// visit 以深度优先次序展开 path。入口处的条数检查对所有分支一视同仁：
// 无论尚未开始扩展还是扩展到一半，只要已收集数达到上限即停止。
func (w *walker) visit(p Path) {
	if w.halted {
		return
	}
	w.ops++ // 条数上限判定：一次比较
	if w.collected == w.req.ResultLimit {
		w.halted = true
		w.recordTruncated(p)
		return
	}
	if p.Depth() == w.req.DepthLimit {
		w.admit(p, true)
		return
	}
	steps := w.sortedSteps(p.EndID())
	if len(steps) == 0 {
		w.admit(p, false)
		return
	}
	for i, st := range steps {
		w.visit(p.Extend(st))
		if w.halted {
			for _, rest := range steps[i+1:] {
				w.recordTruncated(p.Extend(rest))
			}
			return
		}
	}
}

// admit 将一条终结路径计入结果。深度截断的路径只要满足返回条件即计入，
// 是否因条数上限饱和而触发组合标记是独立的另一层判断。
func (w *walker) admit(p Path, depthFired bool) {
	if p.Depth() == 0 {
		// 返回条件：路径跳数 >= 1。起始对象无可用链接时不产生结果。
		return
	}
	w.ops++ // 计数器自增
	w.collected++
	w.ops++ // 条数上限判定：一次比较
	saturated := w.collected == w.req.ResultLimit

	marker := MarkerNone
	var reason string
	switch {
	case depthFired && saturated:
		marker = MarkerDepthThenLimit
		reason = fmt.Sprintf("路径在第 %d 跳触及深度上限(depthLimit=%d)停止扩展；计入第 %d 条结果，恰好使条数上限(resultLimit=%d)饱和，深度先于条数触发",
			p.Depth(), w.req.DepthLimit, w.collected, w.req.ResultLimit)
	case depthFired:
		marker = MarkerDepthOnly
		reason = fmt.Sprintf("路径在第 %d 跳触及深度上限(depthLimit=%d)停止扩展；计入第 %d 条结果，条数上限(resultLimit=%d)未触发",
			p.Depth(), w.req.DepthLimit, w.collected, w.req.ResultLimit)
	default:
		reason = fmt.Sprintf("路径在第 %d 跳自然终结（无可用链接），计入第 %d 条结果，两个上限均未截断该分支",
			p.Depth(), w.collected)
	}
	w.returned = append(w.returned, Branch{Path: p, Returned: true, Marker: marker, Reason: reason})
}

// recordTruncated 记录一条因条数上限饱和而被停止扩展的分支，
// 并判定其未扩展部分在剩余深度预算内是否本可触及深度上限。
func (w *walker) recordTruncated(p Path) {
	marker := MarkerLimitOnly
	var reason string
	rem := w.req.DepthLimit - p.Depth()
	if rem <= 0 || w.reachable(p.EndID(), rem) {
		marker = MarkerLimitThenDepth
		reason = fmt.Sprintf("已收集数达到条数上限(resultLimit=%d)，分支停止扩展；其未扩展部分在剩余 %d 跳预算内本可触及深度上限(depthLimit=%d)，条数先于深度触发",
			w.req.ResultLimit, rem, w.req.DepthLimit)
	} else {
		reason = fmt.Sprintf("已收集数达到条数上限(resultLimit=%d)，分支停止扩展；其未扩展部分在剩余 %d 跳预算内无法触及深度上限(depthLimit=%d)，仅条数触发",
			w.req.ResultLimit, rem, w.req.DepthLimit)
	}
	w.truncated = append(w.truncated, Branch{Path: p, Returned: false, Marker: marker, Reason: reason})
}

type reachKey struct {
	id  string
	rem int
}

// reachable 报告从 id 出发沿允许方向是否存在恰好 rem 跳的游走（带记忆化）。
func (w *walker) reachable(id string, rem int) bool {
	if rem == 0 {
		return true
	}
	key := reachKey{id, rem}
	if v, ok := w.memo[key]; ok {
		return v
	}
	res := false
	for _, st := range w.sortedSteps(id) {
		if w.reachable(st.ToID, rem-1) {
			res = true
			break
		}
	}
	w.memo[key] = res
	return res
}

// sortedSteps 返回从 id 出发的全部可走跳，按 (方向, 链接类型, 对端, 链接 ID)
// 字典序全序排列，保证展开次序确定且可复现。
func (w *walker) sortedSteps(id string) []Step {
	var steps []Step
	for _, dir := range w.dirs {
		for _, l := range w.snap.Links(id, dir) {
			st := Step{Dir: dir, LinkID: l.ID, LinkType: l.Type, FromID: id}
			if dir == graph.Outgoing {
				st.ToID = l.TargetID
			} else {
				st.ToID = l.SourceID
			}
			steps = append(steps, st)
		}
	}
	sort.Slice(steps, func(i, j int) bool { return lessStep(steps[i], steps[j]) })
	return steps
}

func lessStep(a, b Step) bool {
	if a.Dir != b.Dir {
		return a.Dir < b.Dir
	}
	if a.LinkType != b.LinkType {
		return a.LinkType < b.LinkType
	}
	if a.ToID != b.ToID {
		return a.ToID < b.ToID
	}
	return a.LinkID < b.LinkID
}

// sortBranches 将截断分支按路径字典序排序，使输出次序确定。
func sortBranches(bs []Branch) {
	sort.Slice(bs, func(i, j int) bool { return lessPath(bs[i].Path, bs[j].Path) })
}

func lessPath(a, b Path) bool {
	n := len(a.Steps)
	if len(b.Steps) < n {
		n = len(b.Steps)
	}
	for i := 0; i < n; i++ {
		if lessStep(a.Steps[i], b.Steps[i]) {
			return true
		}
		if lessStep(b.Steps[i], a.Steps[i]) {
			return false
		}
	}
	return len(a.Steps) < len(b.Steps)
}
