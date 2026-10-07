package bitemporal

import "sort"

// Engine 执行 AsOf 双时态溯源查询并记录查询日志。
type Engine struct {
	store  *Store
	logger Logger
}

// NewEngine 创建查询引擎。
func NewEngine(store *Store, logger Logger) *Engine {
	return &Engine{store: store, logger: logger}
}

// AsOf 执行一次溯源查询。校验错误按固定次序只报第一类：
//  1. 源对象实例不存在
//  2. 有效时间点 / 写入时间点非法
//  3. 深度上限非正整数
//  4. 写入时间点早于源对象最早写入时间
func (e *Engine) AsOf(q Query) (*Result, error) {
	snap := e.store.Snapshot()
	res, err := asOfView(snap, q)
	snap.Release()
	if e.logger != nil {
		e.logger.Log(QueryLogEntry{Query: q, Result: res, Err: err})
	}
	return res, err
}

// graphView 抽象查询所需的只读数据访问，Snapshot（索引实现）与
// NaiveStore（全量扫描实现）各自实现它，从而强制两条实现走完全相同的
// 判定与遍历代码，随机对照测试只可能因数据差异而不一致。
type graphView interface {
	objectVersions(id string) []ObjectRecord
	linkVersions(id string) []LinkRecord
	outLinks(id string) []LinkRecord
}

func (snap *Snapshot) objectVersions(id string) []ObjectRecord { return snap.ObjectVersions(id) }
func (snap *Snapshot) linkVersions(id string) []LinkRecord     { return snap.LinkVersions(id) }
func (snap *Snapshot) outLinks(id string) []LinkRecord         { return snap.OutLinks(id) }

func asOfView(view graphView, q Query) (*Result, error) {
	sourceVersions := view.objectVersions(q.SourceID)
	if len(sourceVersions) == 0 {
		return nil, &QueryError{Kind: ErrSourceNotFound, Field: "source_id"}
	}
	if q.ValidAt.IsZero() || q.AsOf.IsZero() {
		field := "valid_at"
		if q.AsOf.IsZero() {
			field = "as_of"
		}
		return nil, &QueryError{Kind: ErrInvalidTime, Field: field}
	}
	if q.MaxDepth <= 0 {
		return nil, &QueryError{Kind: ErrInvalidDepth, Field: "max_depth"}
	}
	if q.AsOf.Before(sourceVersions[0].WrittenAt) {
		return nil, &QueryError{Kind: ErrAsOfBeforeEarliestWrite, Field: "as_of"}
	}

	res := &Result{}
	resolved := make(map[string]ObjectRecord)
	resStatus := make(map[string]Status)
	resolveObj := func(id string) (ObjectRecord, Status) {
		if rec, ok := resolved[id]; ok {
			return rec, resStatus[id]
		}
		res.Stats.ObjectsResolved++
		rec, st := resolveObject(view.objectVersions(id), q.ValidAt, q.AsOf)
		resolved[id] = rec
		resStatus[id] = st
		return rec, st
	}

	resolveObj(q.SourceID)

	type frontier struct {
		nodes    []string
		links    []string
		evidence []HopEvidence
	}
	current := []frontier{{nodes: []string{q.SourceID}}}

	for depth := 1; depth <= q.MaxDepth; depth++ {
		type edgeOutcome struct {
			from     frontier
			linkID   string
			rec      LinkRecord
			target   string
			stL, stT Status
			visible  bool
		}
		outcomes := make([]edgeOutcome, 0)
		for _, fr := range current {
			from := fr.nodes[len(fr.nodes)-1]
			linkVersionsByID := make(map[string][]LinkRecord)
			for _, lv := range view.outLinks(from) {
				linkVersionsByID[lv.ID] = append(linkVersionsByID[lv.ID], lv)
			}
			linkIDs := make([]string, 0, len(linkVersionsByID))
			for id := range linkVersionsByID {
				linkIDs = append(linkIDs, id)
			}
			sort.Strings(linkIDs)
			for _, linkID := range linkIDs {
				versions := linkVersionsByID[linkID]
				res.Stats.LinksConsidered += len(versions)
				res.Stats.HopsChecked++
				linkRec, stL := resolveLink(versions, q.ValidAt, q.AsOf)
				target := versions[0].TargetID
				_, stT := resolveObj(target)
				_, stFrom := resolveObj(from)
				combined := combineStatus(stFrom, stL, stT)
				if combined != StatusVisible {
					res.Blocked = append(res.Blocked, BlockedEdge{
						Prefix:       append([]string(nil), fr.nodes...),
						LinkID:       linkID,
						TargetID:     target,
						SourceStatus: stFrom,
						LinkStatus:   stL,
						TargetStatus: stT,
						Combined:     combined,
					})
					continue
				}
				outcomes = append(outcomes, edgeOutcome{
					from: fr, linkID: linkID, rec: linkRec, target: target,
					stL: stL, stT: stT, visible: true,
				})
			}
		}

		// 确定性输出：按完整节点序列排序，消除 map 与调度次序的影响。
		type next struct {
			fr  frontier
			key []string
		}
		nexts := make([]next, 0, len(outcomes))
		for _, oc := range outcomes {
			sourceVerRec, _ := resolveObj(oc.from.nodes[len(oc.from.nodes)-1])
			targetRec, _ := resolveObj(oc.target)
			ev := HopEvidence{
				LinkID: oc.linkID, LinkVersionID: oc.rec.VersionID,
				SourceID: oc.rec.SourceID, SourceVersionID: sourceVerRec.VersionID,
				TargetID: oc.target, TargetVersionID: targetRec.VersionID,
			}
			nodes := append(append([]string(nil), oc.from.nodes...), oc.target)
			links := append(append([]string(nil), oc.from.links...), oc.linkID)
			evidence := append(append([]HopEvidence(nil), oc.from.evidence...), ev)
			nexts = append(nexts, next{fr: frontier{nodes: nodes, links: links, evidence: evidence}, key: nodes})
		}
		sort.Slice(nexts, func(i, j int) bool {
			return lessStringSlice(nexts[i].key, nexts[j].key)
		})

		seen := make(map[string]bool)
		nextFrontier := make([]frontier, 0, len(nexts))
		for _, nx := range nexts {
			pathKey := joinPath(nx.fr.nodes, nx.fr.links)
			if seen[pathKey] {
				continue
			}
			seen[pathKey] = true
			res.Paths = append(res.Paths, Path{
				Nodes: nx.fr.nodes, Links: nx.fr.links, Evidence: nx.fr.evidence,
			})
			// 不延伸回已在当前路径上的节点，消除环导致的无限延伸。
			if depth < q.MaxDepth && !containsString(nx.fr.nodes[:len(nx.fr.nodes)-1], nx.fr.nodes[len(nx.fr.nodes)-1]) {
				nextFrontier = append(nextFrontier, nx.fr)
			}
		}
		current = nextFrontier
	}

	// Blocked 同样按 (前缀, 链接) 确定序输出。
	sort.SliceStable(res.Blocked, func(i, j int) bool {
		c := compareStringSlice(res.Blocked[i].Prefix, res.Blocked[j].Prefix)
		if c != 0 {
			return c < 0
		}
		return res.Blocked[i].LinkID < res.Blocked[j].LinkID
	})
	return res, nil
}

func lessStringSlice(a, b []string) bool { return compareStringSlice(a, b) < 0 }

func compareStringSlice(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func joinPath(nodes, links []string) string {
	out := ""
	for i, n := range nodes {
		if i > 0 {
			out += ">" + links[i-1] + ">"
		}
		out += n
	}
	return out
}
