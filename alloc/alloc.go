// Package alloc 在 node/decider 之上编排两阶段 Reroute 与只读 Explain。
package alloc

import (
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/decider"
	"ontology/node"
)

// Item 是 Assigned/Moved 清单项；Assigned 的 From 为空。
type Item struct {
	Index   string
	Shard   int
	Primary bool
	From    string
	To      string
}

// Result 是一轮 Reroute 的结果，按发生次序排列。
type Result struct {
	Assigned []Item
	Moved    []Item
}

// Verdict 是 Explain 对单个节点的只读判定。
type Verdict struct {
	Node string
	Rule decider.Reason
	Pass bool
}

// ExplainResult 是 Explain 的返回。
type ExplainResult struct {
	Verdicts        []Verdict
	PrimaryNotReady bool
}

// evals 为非导出计数器：最近一次 Reroute 的规则评估次数。
var evals atomic.Int64

// Evals 返回最近一次 Reroute 的规则评估次数（测试用）。
func Evals() int { return int(evals.Load()) }

// Allocator 绑定一个集群状态；所有方法可并发调用，等价于某个串行顺序。
type Allocator struct {
	c *node.Cluster
}

// New 创建绑定集群的分配器。
func New(c *node.Cluster) *Allocator { return &Allocator{c: c} }

// view 适配 node.Cluster 到 decider.View；takeSrc 非空时体现"一份正从该源拿走"。
type view struct {
	c       *node.Cluster
	takeSrc string
	tIndex  string
	tShard  int
	tSize   int64
}

func (v view) Excluded(nid string) bool {
	_, _, _, ex, err := v.c.NodeView(nid)
	return err == nil && ex
}

func (v view) HasShardCopy(nid, index string, s int) bool {
	has, err := v.c.HasShardCopy(nid, index, s)
	if err != nil {
		return false
	}
	if nid == v.takeSrc && index == v.tIndex && s == v.tShard {
		return false
	}
	return has
}

func (v view) ZoneCopyCount(zone, index string, s int) int {
	cnt, err := v.c.ZoneCopyCount(zone, index, s)
	if err != nil {
		return 0
	}
	if index == v.tIndex && s == v.tShard && v.takeSrc != "" {
		if z, _, _, _, err := v.c.NodeView(v.takeSrc); err == nil && z == zone {
			cnt--
		}
	}
	return cnt
}

func (v view) ZoneCount() int { return v.c.ZoneCount() }

func (v view) Used(nid string) int64 {
	_, used, _, _, err := v.c.NodeView(nid)
	if err != nil {
		return 0
	}
	if nid == v.takeSrc {
		used -= v.tSize
	}
	return used
}

func (v view) Total(nid string) int64 {
	_, _, total, _, _ := v.c.NodeView(nid)
	return total
}

func (v view) ZoneOf(nid string) string {
	zone, _, _, _, err := v.c.NodeView(nid)
	if err != nil {
		return ""
	}
	return zone
}

// chooseTarget 在候选节点中按规则选目标：通过全部规则者取份数最少、id 最小；
// skip 非空时不参与（迁出时排除源节点）。
func (a *Allocator) chooseTarget(v view, p decider.Params, skip string, reasons map[string]decider.Reason) (string, bool) {
	best := ""
	bestCnt := 0
	for _, nid := range v.c.SortedNodeIDs() {
		if nid == skip {
			continue
		}
		r := decider.Evaluate(v, nid, p)
		evals.Add(1)
		if reasons != nil {
			reasons[nid] = r
		}
		if r != decider.Pass {
			continue
		}
		cnt, err := v.c.CopyCount(nid)
		if err != nil {
			continue
		}
		if best == "" || cnt < bestCnt {
			best, bestCnt = nid, cnt
		}
	}
	return best, best != ""
}

// Reroute 执行一轮重路由：先放置未分配份，再迁出排除/超高水位节点上的份。
func (a *Allocator) Reroute() (Result, error) {
	a.c.Lock()
	defer a.c.Unlock()

	evals.Store(0)
	res := Result{}

	// ---- 第一阶段：放置未分配的份 ----
	for _, name := range a.c.SortedIndexNames() {
		S, r, size, err := a.c.ShardSpec(name)
		if err != nil {
			return Result{}, err
		}
		c := 1 + r
		for s := 0; s < S; s++ {
			prim, err := a.c.Primary(name, s)
			if err != nil {
				return Result{}, err
			}
			// 主先于副：主未分配则先处理主；本轮主放不下时副本整体跳过。
			if prim == "" {
				p := decider.Params{Index: name, Shard: s, Copies: c, Size: size,
					Primary: true, L: a.c.L, H: a.c.H}
				if dst, ok := a.chooseTarget(view{c: a.c}, p, "", nil); ok {
					if err := a.c.AssignPrimary(name, s, dst); err != nil {
						return Result{}, err
					}
					res.Assigned = append(res.Assigned, Item{Index: name, Shard: s, Primary: true, To: dst})
					prim = dst
				}
			}
			if prim == "" {
				continue // 主未就绪，副本本轮跳过
			}
			reps, _, err := a.c.ReplicaNodes(name, s)
			if err != nil {
				return Result{}, err
			}
			for k := len(reps); k < r; k++ {
				p := decider.Params{Index: name, Shard: s, Copies: c, Size: size,
					Primary: false, L: a.c.L, H: a.c.H}
				dst, ok := a.chooseTarget(view{c: a.c}, p, "", nil)
				if !ok {
					break // 该分片后续副本本轮亦无法放置
				}
				if err := a.c.AssignReplica(name, s, dst); err != nil {
					return Result{}, err
				}
				res.Assigned = append(res.Assigned, Item{Index: name, Shard: s, Primary: false, To: dst})
			}
		}
	}

	// ---- 第二阶段：迁出 ----
	a.relocate(&res)

	if len(res.Assigned) == 0 {
		res.Assigned = []Item{}
	}
	if len(res.Moved) == 0 {
		res.Moved = []Item{}
	}
	return res, nil
}

// relocate 是第二阶段：按 id 字节序考察节点，迁出排除节点全部份与超高水位节点的份。
func (a *Allocator) relocate(res *Result) {
	for _, nid := range a.c.SortedNodeIDs() {
		_, used, total, excluded, err := a.c.NodeView(nid)
		if err != nil {
			continue
		}
		overHigh := func(u int64) bool { return u*100 > int64(a.c.H)*total }
		if !excluded && !overHigh(used) {
			continue
		}
		copies, err := a.c.CopiesOn(nid)
		if err != nil {
			continue
		}
		sortCopies(copies)
		for _, cp := range copies {
			if !excluded {
				_, cur, _, _, _ := a.c.NodeView(nid)
				if !overHigh(cur) {
					break // 一旦不再大于高水位即停
				}
			}
			_, r, _, specErr := a.c.ShardSpec(cp.Index)
			if specErr != nil {
				continue
			}
			p := decider.Params{
				Index: cp.Index, Shard: cp.Shard, Copies: 1 + r, Size: cp.Size,
				Primary: cp.Primary, LowWater: true, L: a.c.L, H: a.c.H,
			}
			v := view{c: a.c, takeSrc: nid, tIndex: cp.Index, tShard: cp.Shard, tSize: cp.Size}
			dst, ok := a.chooseTarget(v, p, nid, nil)
			if !ok {
				continue // 找不到目标的份留在原处，继续看下一份
			}
			if err := a.c.MoveCopy(cp.Index, cp.Shard, nid, dst, cp.Primary); err != nil {
				continue
			}
			res.Moved = append(res.Moved, Item{
				Index: cp.Index, Shard: cp.Shard, Primary: cp.Primary,
				From: nid, To: dst,
			})
		}
	}
}

// sortCopies：size 降序，然后索引名、分片号升序（同分片两份不可能在同节点上）。
func sortCopies(cs []node.Copy) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Size != cs[j].Size {
			return cs[i].Size > cs[j].Size
		}
		if cs[i].Index != cs[j].Index {
			return cs[i].Index < cs[j].Index
		}
		return cs[i].Shard < cs[j].Shard
	})
}

// Explain 只读返回每个节点（id 字节序）对放置一份的第一个否决规则或通过。
func (a *Allocator) Explain(index string, s int, primary bool) (ExplainResult, error) {
	a.c.Lock()
	defer a.c.Unlock()

	_, r, size, err := a.c.ShardSpec(index)
	if err != nil {
		return ExplainResult{}, err
	}
	if !primary && r == 0 {
		return ExplainResult{}, fmt.Errorf("%w: index %q has no replicas", node.ErrInvalid, index)
	}
	if _, err := a.c.Primary(index, s); err != nil {
		return ExplainResult{}, err
	}

	out := ExplainResult{Verdicts: []Verdict{}}
	p := decider.Params{Index: index, Shard: s, Copies: 1 + r, Size: size,
		Primary: primary, L: a.c.L, H: a.c.H}
	v := view{c: a.c}
	for _, nid := range a.c.SortedNodeIDs() {
		reason := decider.Evaluate(v, nid, p)
		out.Verdicts = append(out.Verdicts,
			Verdict{Node: nid, Rule: reason, Pass: reason == decider.Pass})
	}
	if !primary {
		if prim, _ := a.c.Primary(index, s); prim == "" {
			out.PrimaryNotReady = true
		}
	}
	return out, nil
}
