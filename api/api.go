// Package api 对外提供粘滞会话单调读：全局写日志、三副本、会话读与副本切换。
package api

import (
	"errors"
	"sync"

	"ontology/log"
	"ontology/rep"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrEmptyKey = errors.New("api: key 为空")
	ErrBadIndex = errors.New("api: 副本索引越界")
	ErrOffline  = errors.New("api: 目标副本仍 Offline")
)

// API 持有一份全局写日志与三个内存副本；mu 保护全部状态（含会话字段）。
type API struct {
	mu   sync.Mutex
	lg   *log.Log
	reps [3]*rep.Replica
}

// Session 粘滞于某个副本，seen 是本会话见过的最大 applied。
type Session struct {
	replica int
	seen    int
}

// New 返回三副本均 Online 的空系统。
func New() *API {
	return &API{lg: log.New(), reps: [3]*rep.Replica{rep.New(), rep.New(), rep.New()}}
}

// Write 追加一条写并应用到所有 Online 副本，返回 lsn。key 为空返回 ErrEmptyKey。
func (a *API) Write(key, val string) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	lsn := a.lg.Append(key, val)
	for _, r := range a.reps {
		if r.IsOnline() {
			r.Apply(a.lg.At(lsn))
		}
	}
	return lsn, nil
}

func (a *API) Offline(idx int) error { return a.setOnline(idx, false) }
func (a *API) Online(idx int) error  { return a.setOnline(idx, true) }

func (a *API) setOnline(idx int, on bool) error {
	if idx < 0 || idx >= 3 {
		return ErrBadIndex
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reps[idx].SetOnline(on)
	return nil
}

func (a *API) Open(replica int) (*Session, error) {
	if replica < 0 || replica >= 3 {
		return nil, ErrBadIndex
	}
	return &Session{replica: replica}, nil
}

// Read 单调读：先把当前副本补到 seen，再读；之后 applied 必然 >= 旧 seen，直接推进。
func (a *API) Read(s *Session, key string) (string, int, error) {
	if key == "" {
		return "", 0, ErrEmptyKey
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.reps[s.replica]
	if r.Applied() < s.seen {
		r.CatchUp(a.lg, s.seen)
	}
	s.seen = r.Applied()
	return r.Get(key), s.seen, nil
}

// SwitchReplica 把会话切到 newIdx：目标须 Online，先补全到 seen 再改绑定；拒绝不留痕。
func (a *API) SwitchReplica(s *Session, newIdx int) error {
	if newIdx < 0 || newIdx >= 3 {
		return ErrBadIndex
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.reps[newIdx]
	if !r.IsOnline() {
		return ErrOffline
	}
	r.CatchUp(a.lg, s.seen)
	s.replica = newIdx
	return nil
}

func (a *API) View() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]string)
	for _, e := range a.lg.Range(0, a.lg.Len()) {
		out[e.Key] = e.Val
	}
	return out
}

func (a *API) SelfCheck() error {
	b := New()
	w := func(v string) { b.Write("k", v) } // key 非空，err 恒为 nil
	w("A")
	w("B")
	b.Offline(2)
	w("C")
	b.Offline(1)
	w("D")
	w("E")
	b.Online(1)
	b.Online(2)
	s, _ := b.Open(0)
	prev, bad := 0, false
	read := func() { // 不变量1（lsn 不回退）与不变量2（与朴素参照一致）
		v, lsn, err := b.Read(s, "k")
		bad = bad || err != nil || lsn < prev || v != b.View()["k"]
		prev = lsn
	}
	read()
	for _, i := range []int{1, 2} { // 不变量3：切换补全到位，随后 read 的 lsn 不回退
		if err := b.SwitchReplica(s, i); err != nil {
			return err
		}
		read()
	}
	b.Offline(0) // 不变量4：三类拒绝均不留痕
	_, e1 := b.Write("", "x")
	e2 := b.Offline(9)
	e3 := b.SwitchReplica(s, 0)
	b.Online(0)
	v, _, _ := b.Read(s, "k")
	if bad || v != "E" || !errors.Is(e1, ErrEmptyKey) || !errors.Is(e2, ErrBadIndex) || !errors.Is(e3, ErrOffline) {
		return errors.New("selfcheck: 不变量违例")
	}
	return nil
}
