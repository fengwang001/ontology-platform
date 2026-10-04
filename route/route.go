// Package route 按主叫运营商与查询方式给出一次呼叫经过的运营商序列。
package route

import (
	"sync"

	"ontology/numplan"
	"ontology/portdb"
)

// Method 为查询方式。
type Method int

const (
	// ACQ 全呼查询；OR 经归属方转接。
	ACQ Method = iota
	OR
)

// Result 是一次路由查询的结果。Path 为空表示网内呼叫。
type Result struct {
	Home    int
	Serving int
	Ported  bool
	Path    []int
	Probe   numplan.Probe
}

// Router 编排号段库与携转库，承载路由查询入口。
type Router struct {
	mu sync.RWMutex
	db *portdb.DB
}

// New 创建路由器。
func New(db *portdb.DB) *Router { return &Router{db: db} }

// DB 返回底层携转库。
func (r *Router) DB() *portdb.DB { return r.db }

// Query 在 now 时刻查询当前路由，并把全局时钟推进到 now。
func (r *Router) Query(number string, orig int, method Method, now int64) (Result, error) {
	return r.query(number, orig, method, now, false)
}

// QueryAt 只读回答历史时刻 t 的路由；t 晚于已接受的最大 now 报参数非法，不推进时钟。
func (r *Router) QueryAt(number string, orig int, method Method, t int64) (Result, error) {
	return r.query(number, orig, method, t, true)
}

func (r *Router) query(number string, orig int, method Method, t int64, historical bool) (Result, error) {
	if !numplan.ValidNumber(number) || !numplan.ValidOp(orig) ||
		!numplan.ValidTime(t) || (method != ACQ && method != OR) {
		return Result{}, numplan.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dk := r.db.Sync()
	dk.Lock()
	defer dk.Unlock()
	pk := r.db.Plan().Sync()
	pk.Lock()
	defer pk.Unlock()
	plan := r.db.Plan()
	if historical {
		if t > plan.MaxNowLocked() {
			return Result{}, numplan.ErrInvalid
		}
	} else if t < plan.MaxNowLocked() {
		return Result{}, numplan.ErrClockBack
	}
	st, err := r.db.StateAtLocked(number, t)
	if err != nil {
		return Result{}, err
	}
	if st.Frozen {
		return Result{}, numplan.ErrFrozen
	}
	if !historical {
		if err := plan.CheckTimeLocked(t); err != nil {
			return Result{}, err
		}
	}
	res := Result{Home: st.Home, Serving: st.Serving, Ported: st.Ported, Probe: st.Probe}
	res.Path = path(st.Home, st.Serving, orig, method)
	return res, nil
}

// path 是纯函数：
//
//	s==orig 网内 → 空；ACQ → [s]；
//	OR → h==s 或 h==orig 时 [s]，否则 [h,s]。
func path(home, serving, orig int, method Method) []int {
	if serving == orig {
		return []int{}
	}
	if method == ACQ {
		return []int{serving}
	}
	if home == serving || home == orig {
		return []int{serving}
	}
	return []int{home, serving}
}
