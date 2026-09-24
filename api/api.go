// Package api 是广播状态规则版本化的对外入口，依赖 inst（进而依赖 rule），方向单向。
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/inst"
	"ontology/rule"
)

var ErrIllegalRule = rule.ErrIllegalUpdate
var ErrInvalidDeliver = errors.New("api: invalid deliver")
var ErrInvalidKey = errors.New("api: negative key")
var ErrBufferFull = inst.ErrBufferFull

type API struct {
	mu    sync.Mutex
	log   *rule.Log
	insts []*inst.Instance
	out   []inst.Hit
}

func New(P, mb int) *API {
	a := &API{log: rule.NewLog(), insts: make([]*inst.Instance, P)}
	for i := range a.insts {
		a.insts[i] = inst.New(i, mb)
	}
	return a
}
func (a *API) Publish(u rule.Update) error { a.mu.Lock(); defer a.mu.Unlock(); return a.log.Append(u) }
func (a *API) Deliver(i, n int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i < 0 || i >= len(a.insts) || n <= 0 || a.insts[i].V()+n > a.log.Len() {
		return ErrInvalidDeliver
	}
	for k := 0; k < n; k++ {
		a.out = append(a.out, a.insts[i].Apply(a.log.At(a.insts[i].V()+1))...)
	}
	return nil
}
func (a *API) Send(key, val int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if key < 0 {
		return ErrInvalidKey
	}
	x := a.insts[int(key%int64(len(a.insts)))]
	h, e := x.Receive(inst.Data{Key: key, Val: val}, a.log.Len())
	a.out = append(a.out, h...)
	return e
}
func (a *API) Output() []inst.Hit {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]inst.Hit(nil), a.out...)
}
func (a *API) State() (int, []int, []int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	vs, bs := make([]int, len(a.insts)), make([]int, len(a.insts))
	for i, x := range a.insts {
		vs[i], bs[i] = x.V(), x.Buffered()
	}
	return a.log.Len(), vs, bs
}
func (a *API) Versions() (int, []int) { G, vs, _ := a.State(); return G, vs }
func held(a *API) bool {
	for _, x := range a.insts {
		if !x.Invariant(a.log.Len()) {
			return false
		}
	}
	return true
}

type taggedData struct {
	k, v int64
	tag  int
}

func put(id string, th int64) rule.Update { return rule.Put{ID: id, Threshold: th} }
func del(id string) rule.Update           { return rule.Delete{ID: id} }

type stp struct {
	t    byte
	i, n int
	k, v int64
	id   string
	th   int64
}

var twelve = []stp{{'p', 0, 0, 0, 0, "r1", 10}, {'v', 0, 1, 0, 0, "", 0}, {'s', 0, 0, 0, 15, "", 0}, {'s', 0, 0, 1, 12, "", 0}, {'p', 0, 0, 0, 0, "r2", 5}, {'d', 0, 0, 0, 0, "r1", 0}, {'s', 0, 0, 1, 8, "", 0}, {'s', 0, 0, 0, 20, "", 0}, {'v', 1, 3, 0, 0, "", 0}, {'v', 0, 1, 0, 0, "", 0}, {'s', 0, 0, 2, 7, "", 0}, {'v', 0, 1, 0, 0, "", 0}}

func runTwelve(late bool) (*API, []taggedData) {
	a := New(2, 8)
	ops := twelve
	if late {
		ops = append(append([]stp{}, twelve...), stp{t: 'w', i: 0, n: 3}, stp{t: 'w', i: 1, n: 3})
	}
	var s []taggedData
	for _, q := range ops {
		switch {
		case q.t == 'p':
			_ = a.Publish(put(q.id, q.th))
		case q.t == 'd':
			_ = a.Publish(del(q.id))
		case q.t == 'v' && !late, q.t == 'w':
			_ = a.Deliver(q.i, q.n)
		case q.t == 's':
			G, _ := a.Versions()
			if a.Send(q.k, q.v) == nil {
				s = append(s, taggedData{q.k, q.v, G})
			}
		}
	}
	return a, s
}
func naive(a *API, s []taggedData, P int) []inst.Hit {
	var out []inst.Hit
	for _, d := range s {
		for _, id := range a.log.SnapshotAt(d.tag).Match(d.v) {
			out = append(out, inst.Hit{Key: d.k, Val: d.v, RuleID: id, Ver: d.tag, Inst: int(d.k % int64(P))})
		}
	}
	return out
}

// SelfCheck 对内置十二步序列核验第二节四条不变量，全成立返回 true。
func (a *API) SelfCheck() bool {
	c, acc := runTwelve(false)
	late, _ := runTwelve(true)
	G, vs := c.Versions()
	ok := G == 3 && reflect.DeepEqual(vs, []int{3, 3}) && held(c) && held(late) && inst.EqualSet(c.Output(), naive(c, acc, 2)) && inst.EqualSet(c.Output(), late.Output()) && reflect.DeepEqual(inst.ByInst(c.Output(), 0), inst.ByInst(late.Output(), 0)) && reflect.DeepEqual(inst.ByInst(c.Output(), 1), inst.ByInst(late.Output(), 1)) && len(map[error]bool{ErrIllegalRule: true, ErrInvalidDeliver: true, ErrInvalidKey: true, ErrBufferFull: true}) == 4
	bad := func(w error, f func() error) bool {
		g, v := c.Versions()
		o := c.Output()
		e := f() == w
		g2, v2 := c.Versions()
		return e && g == g2 && reflect.DeepEqual(v, v2) && inst.EqualSet(o, c.Output())
	}
	f := New(1, 1)
	_ = f.Publish(put("r", 100))
	full := f.Send(0, 0) == nil && f.Send(0, 0) == ErrBufferFull
	g, v := f.Versions()
	return ok && bad(ErrIllegalRule, func() error { return c.Publish(del("z")) }) && bad(ErrInvalidDeliver, func() error { return c.Deliver(9, 1) }) &&
		bad(ErrInvalidKey, func() error { return c.Send(-1, 1) }) && full && g == 1 && reflect.DeepEqual(v, []int{0}) && len(f.Output()) == 0
}
