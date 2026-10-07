package ontology

import (
	"fmt"
	"sort"
)

// TraverseRequest 描述一次历史快照遍历。
// AsOf 是被固定下来的历史时刻；MaxDepth 与 MaxVisited 是预先声明的上限，
// 一旦遍历需要越过任一上限即整体失败（ErrKindLimitExceeded），
// 不返回掺杂部分结果的不完整快照。
type TraverseRequest struct {
	Start      string
	AsOf       Version
	MaxDepth   int
	MaxVisited int
}

// NodeSnapshot 是遍历到的一个对象在 AsOf 时刻的一致快照。
type NodeSnapshot struct {
	ID         string
	Type       string
	Depth      int
	Props      map[string]Value // 按 AsOf 时刻的属性定义解释：属性名 -> 值
	SchemaFrom Version          // 解释属性所用的定义版本起点（历史基准）
}

// EdgeSnapshot 是 AsOf 时刻实际存在的一条链接。
type EdgeSnapshot struct {
	Type string
	From string
	To   string
}

// Decision 记录遍历中每一次判定的输入、所依据的历史基准与结论，
// 用于事后核查。
type Decision struct {
	Seq       int
	Kind      string // start-check / link-existence / node-resolve / cardinality-anchor / limit-check
	Subject   string
	AsOf      Version
	BasisFrom Version // 判定所依据的历史区间/版本起点
	BasisTo   Version
	Outcome   string
}

// TraverseResult 是一次成功遍历的完整快照与判定日志。
type TraverseResult struct {
	AsOf      Version
	Nodes     []NodeSnapshot
	Edges     []EdgeSnapshot
	Decisions []Decision
}

// Traverse 在固定历史时刻 req.AsOf 上对对象-链接图做一次一致快照遍历。
//
// 错误优先级（多类条件同时具备时只报告一类）：
//  1. 起始对象在 AsOf 尚不存在
//  2. AsOf 早于可回放最早边界
//  3. 深度 / 访问规模超出声明上限
//  4. 底层历史数据缺失
//
// 遍历为只读操作，对历史轨迹无任何可观察改动。
func (s *Store) Traverse(req TraverseRequest) (*TraverseResult, error) {
	tr := &traversal{store: s, req: req, visited: make(map[string]bool)}

	// 优先级 1：起始对象在 AsOf 不存在（仅当 AsOf 可回放时才能判定）。
	if req.AsOf >= s.Horizon() {
		exists, missing := tr.objectExists(req.Start)
		switch {
		case missing:
			tr.pendingMissing = fmt.Sprintf("object-life history of %q missing at v%d", req.Start, req.AsOf)
		case !exists:
			tr.log("start-check", req.Start, 0, 0, "absent")
			return nil, &TraverseError{Kind: ErrKindStartNotExist,
				Detail: fmt.Sprintf("start object %q does not exist at v%d", req.Start, req.AsOf)}
		default:
			tr.log("start-check", req.Start, 0, 0, "present")
		}
	}

	// 优先级 2：AsOf 早于可回放边界。
	if req.AsOf < s.Horizon() {
		return nil, &TraverseError{Kind: ErrKindBeforeHorizon,
			Detail: fmt.Sprintf("asOf v%d is before replay horizon v%d", req.AsOf, s.Horizon())}
	}
	if tr.pendingMissing != "" {
		return nil, &TraverseError{Kind: ErrKindHistoryMissing, Detail: tr.pendingMissing}
	}

	if err := tr.run(); err != nil {
		return nil, err
	}
	return &TraverseResult{
		AsOf:      req.AsOf,
		Nodes:     tr.nodes,
		Edges:     tr.edges,
		Decisions: tr.decisions,
	}, nil
}

type traversal struct {
	store          *Store
	req            TraverseRequest
	visited        map[string]bool
	nodes          []NodeSnapshot
	edges          []EdgeSnapshot
	decisions      []Decision
	pendingMissing string
}

// stepHook 仅供测试注入：在每次节点展开前调用，用于构造
// “长时间遍历与持续并发写入交织”的场景。生产环境恒为 nil。
var stepHook func()

func (t *traversal) log(kind, subject string, from, to Version, outcome string) {
	t.decisions = append(t.decisions, Decision{
		Seq:       len(t.decisions),
		Kind:      kind,
		Subject:   subject,
		AsOf:      t.req.AsOf,
		BasisFrom: from,
		BasisTo:   to,
		Outcome:   outcome,
	})
}

// objectExists 判定对象在 AsOf 是否存在；missing 表示历史数据缺失无法判定。
func (t *traversal) objectExists(id string) (exists, missing bool) {
	s := t.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.gapAt(gapObjectLife, id, t.req.AsOf) {
		return false, true
	}
	oh, ok := s.objects[id]
	if !ok {
		return false, false
	}
	idx, _ := findInterval(oh.life, t.req.AsOf)
	return idx >= 0, false
}

// linkExists 判定候选链接在 AsOf 是否存在。
// 第三个返回值是二分比较次数，用于独立验证单条判定开销。
func (t *traversal) linkExists(lt *linkTypeState, id LinkID) (exists, missing bool, steps int) {
	s := t.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.gapAt(gapLinkType, id.Type, t.req.AsOf) ||
		s.gapAt(gapLink, id.Type+"/"+id.From+"/"+id.To, t.req.AsOf) {
		return false, true, 0
	}
	lh, ok := lt.links[id]
	if !ok {
		return false, false, 1
	}
	ex, steps := lh.existsAt(t.req.AsOf)
	return ex, false, steps
}

// resolveNode 生成对象在 AsOf 的快照；missing 非空表示历史数据缺失及原因。
func (t *traversal) resolveNode(id string, depth int) (NodeSnapshot, string, bool) {
	s := t.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	oh := s.objects[id]
	if s.gapAt(gapObjectProps, id, t.req.AsOf) || s.gapAt(gapSchema, oh.typeID, t.req.AsOf) {
		return NodeSnapshot{}, fmt.Sprintf("property/schema history of %q missing", id), true
	}
	h := s.types[oh.typeID]
	tv, _, ok := h.versionAt(t.req.AsOf)
	if !ok {
		return NodeSnapshot{}, fmt.Sprintf("no schema version covers v%d", t.req.AsOf), true
	}
	// 只记录不可变的版本起点：区间右端点可能被后续迁移闭合，
	// 记录它会让判定日志受遍历期间并发变更的影响。
	t.log("schema-select", oh.typeID, tv.From, 0,
		fmt.Sprintf("schema version from v%d anchors property interpretation", tv.From))
	props := make(map[string]Value, len(tv.Props))
	for _, pd := range tv.Props {
		chain := oh.props[pd.ID]
		ivs := make([]Interval, len(chain))
		for i, pv := range chain {
			ivs[i] = pv.Interval
		}
		idx, _ := findInterval(ivs, t.req.AsOf)
		if idx >= 0 {
			props[pd.Name] = chain[idx].Val
		}
	}
	return NodeSnapshot{ID: id, Type: oh.typeID, Depth: depth, Props: props, SchemaFrom: tv.From}, "", false
}

func (t *traversal) run() error {
	asOf := t.req.AsOf
	type item struct {
		id    string
		depth int
	}
	queue := []item{{id: t.req.Start, depth: 0}}
	t.visited[t.req.Start] = true

	visit := func(id string, depth int) error {
		if len(t.visited) > t.req.MaxVisited {
			t.log("limit-check", id, 0, 0, "visited-limit-exceeded")
			return &TraverseError{Kind: ErrKindLimitExceeded,
				Detail: fmt.Sprintf("visited count exceeds declared limit %d", t.req.MaxVisited)}
		}
		snap, missingDetail, missing := t.resolveNode(id, depth)
		if missing {
			return &TraverseError{Kind: ErrKindHistoryMissing,
				Detail: fmt.Sprintf("cannot resolve object %q at v%d: %s", id, asOf, missingDetail)}
		}
		t.log("node-resolve", id, snap.SchemaFrom, 0, "resolved")
		t.nodes = append(t.nodes, snap)
		return nil
	}

	if err := visit(t.req.Start, 0); err != nil {
		return err
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if stepHook != nil {
			stepHook()
		}

		s := t.store
		s.mu.RLock()
		// 收集 (链接类型, 候选链接) 并确定类型顺序，保证展开顺序确定。
		var typeIDs []string
		for typeID, lt := range s.linkTys {
			if len(lt.out[cur.id]) > 0 {
				typeIDs = append(typeIDs, typeID)
			}
		}
		sort.Strings(typeIDs)
		cands := make(map[string][]LinkID, len(typeIDs))
		for _, typeID := range typeIDs {
			for _, id := range s.linkTys[typeID].out[cur.id] {
				// 跳过在 AsOf 之后才首次创建的候选：
				// 保证判定日志不受遍历期间新写入的影响。
				ivs := s.linkTys[typeID].links[id].intervals
				if len(ivs) > 0 && ivs[0].From <= asOf {
					cands[typeID] = append(cands[typeID], id)
				}
			}
		}
		s.mu.RUnlock()

		// 三阶段展开：先评估全部候选链接的存在性（记录缺失但不立即返回），
		// 再按声明上限检查（上限错误优先于缺失错误），最后才报告缺失。
		type presentEdge struct {
			id LinkID
		}
		var present []presentEdge
		var missingErr *TraverseError
		for _, typeID := range typeIDs {
			lt := func() *linkTypeState {
				s.mu.RLock()
				defer s.mu.RUnlock()
				return s.linkTys[typeID]
			}()
			// 记录恰好覆盖 AsOf 的基数约束版本（遍历不按其过滤，仅锚定）。
			s.mu.RLock()
			cv, _, ok := lt.cardinalityAt(asOf)
			s.mu.RUnlock()
			if ok {
				t.log("cardinality-anchor", typeID, cv.From, 0,
					fmt.Sprintf("cardinality %s anchored at version from v%d; not used to filter", cv.Card, cv.From))
			}
			for _, id := range cands[typeID] {
				exists, missing, _ := t.linkExists(lt, id)
				if missing {
					if missingErr == nil {
						missingErr = &TraverseError{Kind: ErrKindHistoryMissing,
							Detail: fmt.Sprintf("cannot determine link %v at v%d", id, asOf)}
					}
					continue
				}
				if !exists {
					t.log("link-existence", id.Type+"/"+id.From+"->"+id.To, 0, 0, "absent")
					continue
				}
				t.log("link-existence", id.Type+"/"+id.From+"->"+id.To, 0, 0, "present")
				present = append(present, presentEdge{id: id})
			}
		}

		for _, pe := range present {
			id := pe.id
			t.edges = append(t.edges, EdgeSnapshot{Type: id.Type, From: id.From, To: id.To})

			// 目标对象在 AsOf 必须也存在，否则只记边不展开。
			tex, tmissing := t.objectExists(id.To)
			if tmissing && missingErr == nil {
				missingErr = &TraverseError{Kind: ErrKindHistoryMissing,
					Detail: fmt.Sprintf("object-life history of %q missing at v%d", id.To, asOf)}
			}
			if tmissing {
				continue
			}
			if !tex {
				t.log("node-resolve", id.To, 0, 0, "target-absent")
				continue
			}
			if t.visited[id.To] {
				continue
			}
			if cur.depth+1 > t.req.MaxDepth {
				t.log("limit-check", id.To, 0, 0, "depth-limit-exceeded")
				return &TraverseError{Kind: ErrKindLimitExceeded,
					Detail: fmt.Sprintf("traversal depth exceeds declared limit %d", t.req.MaxDepth)}
			}
			t.visited[id.To] = true
			if err := visit(id.To, cur.depth+1); err != nil {
				return err
			}
			queue = append(queue, item{id: id.To, depth: cur.depth + 1})
		}
		if missingErr != nil {
			return missingErr
		}
	}
	return nil
}
