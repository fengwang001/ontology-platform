package djoin

import (
	"errors"
	"sync"

	"ontology/rel"
)

// 三类可判定哨兵错误，互不相同；被拒批次不留任何状态痕迹。
var (
	ErrDeleteMissing = errors.New("djoin: deleting a row that does not exist")
	ErrInvalidChange = errors.New("djoin: invalid change (Sign must be ±1, V non-empty)")
	ErrViewLimit     = errors.New("djoin: materialized view would exceed maxView distinct tuples")
)

type Delta struct {
	K    int64
	A, B string
	Mult int64 // 非零带符号多重性，下游把它加到 (K,A,B) 上
}

type tkey struct {
	k    int64
	a, b string
}

type agg map[int64]map[string]int64

type Engine struct {
	mu      sync.RWMutex
	r, s    *rel.Table
	view    map[tkey]int64
	maxView int
	checked int64 // 非导出：最近一次 Feed 为三项差分检查过的对侧表（含 Δ 侧）行数
}

func New(maxView int) *Engine {
	return &Engine{r: rel.New(), s: rel.New(), view: map[tkey]int64{}, maxView: maxView}
}
func aggregate(rows []rel.Row) (agg, error) {
	m := agg{}
	for _, x := range rows {
		if (x.Sign != 1 && x.Sign != -1) || x.V == "" {
			return nil, ErrInvalidChange
		}
		bk := m[x.K]
		if bk == nil {
			bk = map[string]int64{}
			m[x.K] = bk
		}
		bk[x.V] += int64(x.Sign)
	}
	return m, nil
}

// terms 算齐三项（对侧表均取批前）：ΔR⋈S旧 + R旧⋈ΔS + ΔR⋈ΔS，并累计 checked。
func (e *Engine) terms(dr, ds agg) map[tkey]int64 {
	out := map[tkey]int64{}
	put := func(k int64, a, b string, m int64) {
		if m == 0 {
			return
		}
		key := tkey{k, a, b}
		if out[key] += m; out[key] == 0 { // 同元组合并归零即不出现在输出中
			delete(out, key)
		}
	}
	var n int64
	for k, ra := range dr {
		e.s.Match(k, func(b string, sm int64) { // T1：ΔR ⋈ S_old，对侧只取 K 桶
			n++
			for a, ad := range ra {
				put(k, a, b, ad*sm)
			}
		})
		if sb := ds[k]; sb != nil { // T3：ΔR ⋈ ΔS，只碰两侧都改到的 K
			for a, ad := range ra {
				for b, bd := range sb {
					n++
					put(k, a, b, ad*bd)
				}
			}
		}
	}
	for k, sb := range ds {
		e.r.Match(k, func(a string, rm int64) { // T2：R_old ⋈ ΔS
			n++
			for b, bd := range sb {
				put(k, a, b, rm*bd)
			}
		})
	}
	e.checked = n
	return out
}

// Feed 原子吃下一批：先校验 Sign/V、非负、maxView（只预演不提交），
// 全过后才在同一把锁内提交 R/S/视图；任一项失败状态不变。
func (e *Engine) Feed(dR, dS []rel.Row) ([]Delta, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dr, e1 := aggregate(dR)
	ds, e2 := aggregate(dS)
	if e1 != nil {
		return nil, e1
	}
	if e2 != nil {
		return nil, e2
	}
	if negative(dr, e.r) || negative(ds, e.s) {
		return nil, ErrDeleteMissing
	}
	d := e.terms(dr, ds)
	proj := len(e.view)
	for key, m := range d { // 预演批后非零不同元组数
		old := e.view[key]
		if old == 0 {
			proj++
		} else if old+m == 0 {
			proj--
		}
	}
	if proj > e.maxView {
		return nil, ErrViewLimit
	}
	apply(dr, e.r)
	apply(ds, e.s)
	for key, m := range d {
		if e.view[key] += m; e.view[key] == 0 { // 视图元组归零即删除
			delete(e.view, key)
		}
	}
	return sorted(d), nil
}

// View 返回有序副本；RWMutex 保证看到的必是某个完整批次之后的状态。
func (e *Engine) View() []Delta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return sorted(e.view)
}
