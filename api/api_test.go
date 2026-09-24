package api

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/kv"
	"ontology/schema"
)

var errTestBoom = errors.New("boom-test")

// TestNaiveReference：随机操作序列下，公开 API 的 Read 逐次与朴素参照一致。
// 朴素参照只记每次 Write 的原始 (版本,字节)，Read 总从原始版本重跑整链、从不写回。
func TestNaiveReference(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99} { // 表驱动：多个确定性种子
		a := New()
		rng := rand.New(rand.NewSource(seed))
		fns := map[int]MigFn{}
		for f := 1; f <= 4; f++ {
			f := f
			fns[f] = func(b []byte) ([]byte, error) {
				if f == 2 && len(b) > 0 && b[0] == 'X' { // 第2步对某些数据确定性失败
					return nil, errors.New("x")
				}
				return append(append([]byte(nil), b...), byte('a'+f-1)), nil
			}
		}
		V := 1
		registered := map[int]bool{}
		orig := map[string]ne{} // 朴素参照：最后一次 Write 的原始版本与字节
		keys := []string{"p", "q", "r"}
		nread := func(k string) ([]byte, string) { // 朴素参照的读取
			e, ok := orig[k]
			if !ok {
				return nil, "notfound"
			}
			out := append([]byte(nil), e.raw...)
			for from := e.v; from < V; from++ {
				if !registered[from] {
					return nil, "missing"
				}
				r, err := fns[from](out)
				if err != nil {
					return nil, "failed"
				}
				out = r
			}
			return out, "ok"
		}
		for step := 0; step < 300; step++ {
			switch rng.Intn(5) {
			case 0: // 登记一个步骤（概率跳过以制造缺链）
				f := 1 + rng.Intn(4)
				if !registered[f] && rng.Intn(3) > 0 {
					if e := a.Register(f, fns[f]); e != nil {
						t.Fatal(e)
					}
					registered[f] = true
				}
			case 1: // 升级；to<=V 必须被拒且朴素侧不变
				to := V + rng.Intn(3)
				if to <= V {
					if e := a.Upgrade(to); !errors.Is(e, kv.ErrInvalidVersion) {
						t.Fatal(e)
					}
					continue
				}
				if e := a.Upgrade(to); e != nil {
					t.Fatal(e)
				}
				V = to
			case 2, 3: // 写入（偶发产出会在第2步失败的数据）
				k := keys[rng.Intn(len(keys))]
				v := []byte{'a' + byte(rng.Intn(26))}
				if rng.Intn(4) == 0 {
					v[0] = 'X'
				}
				a.Write(k, v)
				orig[k] = ne{V, append([]byte(nil), v...)}
			case 4:
				k := keys[rng.Intn(len(keys))]
				got, gerr := a.Read(k)
				want, wc := nread(k)
				if errClass(gerr) != wc || gerr == nil && string(got) != string(want) {
					t.Fatalf("seed=%d step=%d %s: got(%q,%s) want(%q,%s)",
						seed, step, k, got, errClass(gerr), want, wc)
				}
			}
		}
	}
}
func TestAPIErrors(t *testing.T) {
	noop := func(b []byte) ([]byte, error) { return b, nil }
	cases := []struct {
		name string
		prep func(a *API)
		call func(a *API) error
		want error
	}{
		{"upgrade not greater", nil, func(a *API) error { return a.Upgrade(1) }, kv.ErrInvalidVersion},
		{"register from<1", nil, func(a *API) error { return a.Register(0, noop) }, schema.ErrInvalidRegister},
		{"register nil fn", nil, func(a *API) error { return a.Register(1, nil) }, schema.ErrInvalidRegister},
		{"register duplicate", func(a *API) { _ = a.Register(1, noop) },
			func(a *API) error { return a.Register(1, noop) }, schema.ErrInvalidRegister},
		{"key not found", nil, func(a *API) error { _, e := a.Read("zz"); return e }, kv.ErrKeyNotFound},
		{"missing migration", nil, func(a *API) error {
			a.Write("k", []byte("a"))
			_ = a.Upgrade(2)
			_, e := a.Read("k")
			return e
		}, schema.ErrMissingMigration},
		{"migration failed", nil, func(a *API) error {
			_ = a.Register(1, func(b []byte) ([]byte, error) { return nil, errTestBoom })
			a.Write("k", []byte("a"))
			_ = a.Upgrade(2)
			_, e := a.Read("k")
			return e
		}, schema.ErrMigrationFailed},
	}
	for _, c := range cases {
		a := New()
		if c.prep != nil {
			c.prep(a)
		}
		e := c.call(a)
		if !errors.Is(e, c.want) {
			t.Fatalf("%s: %v not %v", c.name, e, c.want)
		}
		if c.want == schema.ErrMigrationFailed && !errors.Is(e, errTestBoom) {
			t.Fatalf("%s: raw cause lost", c.name)
		}
	}
	// 被拒后状态不变、仍可继续使用：缺迁移 → 补登记 → 同一次 Read 成功。
	a := New()
	a.Write("k", []byte("a"))
	_ = a.Upgrade(2)
	_, e := a.Read("k")
	v, r, _ := a.Stored("k")
	if !errors.Is(e, schema.ErrMissingMigration) || v != 1 || string(r) != "a" {
		t.Fatalf("after reject: err=%v stored=(%d,%q)", e, v, r)
	}
	if e := a.Register(1, func(b []byte) ([]byte, error) { return append(b, '2'), nil }); e != nil {
		t.Fatal(e)
	}
	if v, e := a.Read("k"); e != nil || string(v) != "a2" {
		t.Fatalf("recover read=(%q,%v)", v, e)
	}
}
