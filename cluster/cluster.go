// Package cluster 按 (长度, 首词) 叶子与相似度阈值把日志掩码序列归并成模板，
// 并强制每租户模板数上限与租户数上限。
package cluster

import (
	"strings"
	"sync"

	"ontology/token"
)

// Merger 是并发安全的模板归并器。
type Merger struct {
	mu      sync.Mutex
	theta   int
	tmax    int
	nt      int
	gen     int64
	tenants map[string]*tenantState

	// comparedTotal 为非导出计数器：累计每次 Ingest 在所属叶子比较的模板数。
	comparedTotal int64
}

// New 构造归并器。theta 为百分数 1..100；tmax 为 1..1e5；nt 为 1..1e4。
func New(theta, tmax, nt int) (*Merger, error) {
	if theta < 1 || theta > 100 || tmax < 1 || tmax > 100000 || nt < 1 || nt > 10000 {
		return nil, ErrInvalidArgument
	}
	return &Merger{
		theta:   theta,
		tmax:    tmax,
		nt:      nt,
		tenants: make(map[string]*tenantState),
	}, nil
}

// SetTheta 热更新阈值；非法值被拒绝且不推进代数。
func (m *Merger) SetTheta(theta int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if theta < 1 || theta > 100 {
		return ErrInvalidArgument
	}
	m.theta = theta
	m.gen++
	return nil
}

// Gen 返回全局阈值代数（初值 0，每次成功 SetTheta 加 1）。
func (m *Merger) Gen() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// Ingest 归并一条日志，返回命中/新建/溢出结果。
// 拒绝顺序固定为：参数非法 → 租户上限；被拒绝操作不改任何状态。
func (m *Merger) Ingest(tenant, msg string) (Result, error) {
	if !token.ValidTenant(tenant) {
		return Result{}, ErrInvalidArgument
	}
	tok, err := token.Tokenize(msg)
	if err != nil {
		return Result{}, ErrInvalidArgument
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.tenants[tenant]
	if !ok {
		if len(m.tenants) >= m.nt {
			return Result{}, ErrTenantLimit
		}
		st = &tenantState{leaves: make(map[leafKey]*leaf)}
		m.tenants[tenant] = st
	}

	key := leafKey{n: len(tok), first: tok[0]}
	lf := st.leaves[key]
	if lf == nil {
		lf = &leaf{}
		st.leaves[key] = lf
	}
	m.comparedTotal += int64(len(lf.tpls))

	best, bestEq := pick(lf, tok, m.theta)
	if best != nil {
		generalize(best, tok)
		best.count++
		return Result{ID: best.id, Matched: true, Compared: len(lf.tpls), Eq: bestEq}, nil
	}
	if st.count >= m.tmax {
		st.overflow++
		return Result{Overflow: true, Compared: len(lf.tpls), Eq: bestEq}, nil
	}
	st.nextID++
	st.count++
	tp := &tpl{id: st.nextID, words: append([]string(nil), tok...), count: 1, gen: m.gen}
	lf.tpls = append(lf.tpls, tp)
	return Result{ID: tp.id, Created: true, Compared: len(lf.tpls), Eq: bestEq}, nil
}

// pick 在叶子内选出合格模板，返回 nil 表示无合格模板。
// 规则：eq*100 >= n*theta 合格；eq 最大、通配最少、id 最小依次决胜。
func pick(lf *leaf, tok []string, theta int) (*tpl, int) {
	n := len(tok)
	var best *tpl
	bestEq := 0
	bestWild := 0
	for _, tp := range lf.tpls {
		eq := 0
		wild := 0
		for i, w := range tp.words {
			if w == token.Wildcard {
				wild++
				eq++
			} else if w == tok[i] {
				eq++
			}
		}
		if eq*100 < n*theta {
			continue
		}
		if best == nil || eq > bestEq || (eq == bestEq && wild < bestWild) ||
			(eq == bestEq && wild == bestWild && tp.id < best.id) {
			best, bestEq, bestWild = tp, eq, wild
		}
	}
	return best, bestEq
}

// generalize 把模板中既不等于 tok 又不是通配的位置单向改为通配。
func generalize(tp *tpl, tok []string) {
	for i, w := range tp.words {
		if w != token.Wildcard && w != tok[i] {
			tp.words[i] = token.Wildcard
		}
	}
}

// Snapshot 返回某租户按 id 升序的模板视图与溢出桶计数。
func (m *Merger) Snapshot(tenant string) ([]TemplateInfo, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.tenants[tenant]
	if !ok {
		return nil, 0, ErrNoSuchTenant
	}
	out := make([]TemplateInfo, 0, st.count)
	for _, lf := range st.leaves {
		for _, tp := range lf.tpls {
			out = append(out, TemplateInfo{
				ID:    tp.id,
				Text:  append([]string(nil), tp.words...),
				Count: tp.count,
				Gen:   tp.gen,
			})
		}
	}
	sortByID(out)
	return out, st.overflow, nil
}

func sortByID(ts []TemplateInfo) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j-1].ID > ts[j].ID; j-- {
			ts[j-1], ts[j] = ts[j], ts[j-1]
		}
	}
}

// comparedCount 暴露非导出计数器，供同包测试断言。
func (m *Merger) comparedCount() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.comparedTotal
}

// join 便于测试与 budget 包拼接模板文本。
func join(words []string) string { return strings.Join(words, " ") }
