// Package join 增量维护三表自然连接 R(a,b)⋈S(b,c)⋈T(c,d) 的结果多重集，依赖 rel。
package join

import (
	"errors"
	"maps"
	"sync"

	"ontology/rel"
)

// Quad 是连接结果四元组 (a,b,c,d)。
type Quad struct{ A, B, C, D int }

// Join 持有三表、b/c 连接索引与物化结果多重集。
type Join struct {
	mu                             sync.Mutex
	r, s, t                        *rel.Relation
	rByB, sByB, sByC, tByC         map[int]map[rel.Tuple]int
	res                            map[Quad]int
	sz, maxResults, lastCandidates int // 条数合计 / 上限 / 非导出候选对数
}

// New 创建空维护器；maxResults>0 时结果总条数不得超过它。
func New(maxResults int) *Join {
	return &Join{r: rel.New(), s: rel.New(), t: rel.New(),
		rByB: map[int]map[rel.Tuple]int{}, sByB: map[int]map[rel.Tuple]int{},
		sByC: map[int]map[rel.Tuple]int{}, tByC: map[int]map[rel.Tuple]int{},
		res: map[Quad]int{}, maxResults: maxResults}
}

// Insert 先只读地算增量并预检上限，全部通过后才落盘（失败不留痕）。
func (j *Join) Insert(tab string, x rel.Tuple) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	d, added, err := j.delta(tab, x)
	if err != nil {
		return err
	}
	if j.maxResults > 0 && j.sz+added > j.maxResults {
		return ErrTooMany // 此刻尚未改动任何状态
	}
	for q, n := range d {
		j.res[q] += n
		j.sz += n
	}
	j.rel(tab).Insert(x)
	j.index(tab, x, 1)
	return nil
}

// Delete 删除一条元组（计数 -1），恰好扣减它与其它两表全部匹配产生的结果。
func (j *Join) Delete(tab string, x rel.Tuple) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	d, _, err := j.delta(tab, x) // delta 对非法表名返回 ErrBadTable，不改三表
	if err != nil {
		return err
	}
	if j.rel(tab).Count(x) == 0 {
		return rel.ErrNotFound // 拒绝于任何改动之前
	}
	for q, n := range d {
		j.res[q] -= n
		j.sz -= n
		if j.res[q] == 0 {
			delete(j.res, q)
		}
	}
	_ = j.rel(tab).Delete(x) // 上面已确认计数 >0
	j.index(tab, x, -1)
	return nil
}

// Result 返回结果多重集快照：四元组 → 多重度。
func (j *Join) Result() map[Quad]int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return maps.Clone(j.res)
}

// delta 沿 b/c 索引枚举被改元组与其它两表全部匹配（权重为两侧副本数之积），并登记候选对数（非导出）。
func (j *Join) delta(tab string, x rel.Tuple) (map[Quad]int, int, error) {
	d := map[Quad]int{}
	added := 0
	j.lastCandidates = 0
	put := func(q Quad, w int) { d[q] += w; added += w }
	switch tab {
	case "R": // x=(a,b)：S 按 b、T 按 s.c
		for s, cs := range j.sByB[x.Y] {
			for tt, ct := range j.tByC[s.Y] {
				j.lastCandidates++
				put(Quad{x.X, x.Y, s.Y, tt.Y}, cs*ct)
			}
		}
	case "S": // x=(b,c)：R 按 b、T 按 c
		for r, cr := range j.rByB[x.X] {
			for tt, ct := range j.tByC[x.Y] {
				j.lastCandidates++
				put(Quad{r.X, x.X, x.Y, tt.Y}, cr*ct)
			}
		}
	case "T": // x=(c,d)：S 按 c、R 按 s.b
		for s, cs := range j.sByC[x.X] {
			for r, cr := range j.rByB[s.X] {
				j.lastCandidates++
				put(Quad{r.X, s.X, x.X, x.Y}, cr*cs)
			}
		}
	default:
		return nil, 0, ErrBadTable
	}
	return d, added, nil
}

// rel 按表名取关系；调用方均已校验表名合法。
func (j *Join) rel(tab string) *rel.Relation {
	return map[string]*rel.Relation{"R": j.r, "S": j.s, "T": j.t}[tab]
}

// index 以 sign(±1) 维护被改元组所在的索引分组，空组即时删除。
func (j *Join) index(tab string, x rel.Tuple, sign int) {
	add := func(m map[int]map[rel.Tuple]int, key int) {
		g := m[key]
		if g == nil {
			g = map[rel.Tuple]int{}
			m[key] = g
		}
		if g[x] += sign; g[x] == 0 {
			delete(g, x)
			if len(g) == 0 {
				delete(m, key)
			}
		}
	}
	switch tab {
	case "S":
		add(j.sByB, x.X)
		add(j.sByC, x.Y)
	case "R":
		add(j.rByB, x.Y)
	default:
		add(j.tByC, x.X)
	}
}

var (
	ErrTooMany  = errors.New("join: result size exceeds maxResults") // 结果条数将超限
	ErrBadTable = errors.New("join: unknown relation (want R/S/T)")  // 表名非法
)
