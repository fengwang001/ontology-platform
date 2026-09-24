// Package api 是惰性 schema 迁移键控状态的对外入口，依赖 kv（进而依赖 schema）。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/kv"
	"ontology/schema"
)

type MigFn = func([]byte) ([]byte, error)

type API struct {
	reg *schema.Registry
	st  *kv.Store
}

func New() *API { r := schema.NewRegistry(); return &API{r, kv.New(r)} }

func (a *API) Register(from int, fn MigFn) error      { return a.reg.Register(from, fn) }
func (a *API) Upgrade(to int) error                   { return a.st.Upgrade(to) }
func (a *API) Version() int                           { return a.st.Version() }
func (a *API) Write(key string, data []byte)          { a.st.Write(key, data) }
func (a *API) Read(key string) ([]byte, error)        { return a.st.Read(key) }
func (a *API) Stored(key string) (int, []byte, error) { return a.st.Stored(key) }

// 朴素参照：只记每次 Write 的原始 (版本,字节)，Read 总从原始版本重跑整链、从不写回。
type ne struct {
	v   int
	raw []byte
}
type naiveRef struct {
	fns map[int]MigFn
	V   int
	m   map[string]ne
}

func newNaive() *naiveRef { return &naiveRef{map[int]MigFn{}, 1, map[string]ne{}} }

func (n *naiveRef) read(key string) ([]byte, error) {
	e, ok := n.m[key]
	if !ok {
		return nil, kv.ErrKeyNotFound
	}
	out := append([]byte(nil), e.raw...)
	for from := e.v; from < n.V; from++ {
		fn, ok := n.fns[from]
		if !ok {
			return nil, schema.ErrMissingMigration
		}
		r, err := fn(out)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", schema.ErrMigrationFailed, err)
		}
		out = r
	}
	return out, nil
}

func errClass(err error) string {
	for _, c := range []struct {
		e    error
		name string
	}{
		{schema.ErrMissingMigration, "missing"}, {schema.ErrMigrationFailed, "failed"}, {kv.ErrKeyNotFound, "notfound"},
		{kv.ErrInvalidVersion, "badversion"}, {schema.ErrInvalidRegister, "badregister"},
	} {
		if errors.Is(err, c.e) {
			return c.name
		}
	}
	return "ok"
}

var (
	mk1 MigFn = func(b []byte) ([]byte, error) { return append(b, ',', '2'), nil }
	mk2 MigFn = func(b []byte) ([]byte, error) {
		if len(b) >= 3 && string(b[:3]) == "bad" {
			return nil, errors.New("bad prefix")
		}
		return []byte(strings.ReplaceAll(string(b), ",", ";")), nil
	}
	mk3 MigFn = func(b []byte) ([]byte, error) { return append([]byte("4:"), b...), nil }
)

func counted(calls *[4]int, i int, f MigFn) MigFn {
	return func(b []byte) ([]byte, error) { calls[i]++; return f(b) }
}
func (a *API) SelfCheck() error {
	// 在独立状态上跑第三节内置序列并对照朴素参照核验四条不变量；不触碰调用方状态。
	a = New()
	var calls [4]int
	a.Register(1, counted(&calls, 0, mk1))
	a.Register(2, counted(&calls, 1, mk2))
	n := newNaive()
	n.fns[1], n.fns[2] = mk1, mk2
	w := func(k, v string) { a.Write(k, []byte(v)); n.m[k] = ne{n.V, []byte(v)} }
	cmp := func(ks ...string) error {
		for _, k := range ks {
			g, ge := a.Read(k)
			z, ze := n.read(k)
			if errClass(ge) != errClass(ze) || ge == nil && string(g) != string(z) {
				return fmt.Errorf("%s got(%q,%s) want(%q,%s)", k, g, errClass(ge), z, errClass(ze))
			}
		}
		return nil
	}
	w("k1", "a")
	w("k2", "bad")
	if e := a.Upgrade(3); e != nil {
		return e
	}
	n.V = 3
	w("k3", "c,d")
	if e := cmp("k1", "k2"); e != nil {
		return e
	}
	if e := a.Upgrade(4); e != nil {
		return e
	}
	n.V = 4
	if _, e := a.Read("k1"); errClass(e) != "missing" { // 第8步：缺 m3
		return fmt.Errorf("step 8 want missing, got %v", e)
	}
	a.Register(3, counted(&calls, 2, mk3))
	n.fns[3] = mk3
	if e := cmp("k1", "k3", "k1"); e != nil {
		return e
	}
	if _, nk := a.Read("nope"); errClass(nk) != "notfound" {
		return fmt.Errorf("missing-key %v", nk)
	}
	if calls != [4]int{2, 2, 2, 0} {
		return fmt.Errorf("invariant2 calls=%v want [2 2 2 0]", calls)
	}
	if v, r, e := a.Stored("k2"); e != nil || v != 1 || string(r) != "bad" {
		return fmt.Errorf("invariant3/4 k2=(%d,%q,%v)", v, r, e)
	}
	for i, e := range []error{a.Upgrade(1), a.Register(1, mk1), a.Register(0, mk1), a.Register(1, nil)} {
		if e == nil || errClass(e) != "badversion" && errClass(e) != "badregister" {
			return fmt.Errorf("invariant4 rejected op %d: %v", i, e)
		}
	}
	if a.Version() != 4 {
		return fmt.Errorf("invariant4 V=%d", a.Version())
	}
	return nil
}
