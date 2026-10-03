package negotiate

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/adapter"
)

// 本文件把实现与一份按规则逐条写成的朴素模拟对照：
// 2000 组随机操作序列，每个操作都比对双方结果与注册表版本号，
// 并在日志中打印输入、输出与判定依据（go test -v 可见）。

// model 是朴素模拟：独立实现规则，每次求值都从 map 现算。
type model struct {
	h        int
	steps    map[int][2]bool // v -> {reqLossy, respLossy}
	sunsets  map[int]int64
	previews map[int]string
	ver      uint64
}

func newModel(h int) *model {
	return &model{
		h:        h,
		steps:    make(map[int][2]bool),
		sunsets:  make(map[int]int64),
		previews: make(map[int]string),
	}
}

// mutResult 对应 adapter.Kind：0 成功，1 参数非法，2 对象不存在。
type mutResult int

const (
	mutOK mutResult = iota
	mutInvalid
	mutNotFound
)

func (m *model) setAdapter(v int, req, resp bool) mutResult {
	if v < 1 || v >= m.h {
		return mutInvalid
	}
	m.steps[v] = [2]bool{req, resp}
	m.ver++
	return mutOK
}

func (m *model) removeAdapter(v int) mutResult {
	if v < 1 || v >= m.h {
		return mutInvalid
	}
	if _, ok := m.steps[v]; !ok {
		return mutNotFound
	}
	delete(m.steps, v)
	m.ver++
	return mutOK
}

func (m *model) sunset(v int, t int64) mutResult {
	if v < 1 || v >= m.h || t < 0 || t > adapter.MaxTime {
		return mutInvalid
	}
	m.sunsets[v] = t
	m.ver++
	return mutOK
}

func (m *model) preview(v int, scope string) mutResult {
	if v < 1 || v > m.h || scope == "" {
		return mutInvalid
	}
	m.previews[v] = scope
	m.ver++
	return mutOK
}

// parseRange 是朴素解析器，按文法逐条判断。
func parseRange(s string) (lo, hi int, ok bool) {
	num := func(p string) (int, bool) {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return 0, false
		}
		n := 0
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return 0, false
			}
			n = n*10 + int(p[i]-'0')
			if n > 1000 {
				return 0, false
			}
		}
		if n < 1 {
			return 0, false
		}
		return n, true
	}
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		n, ok := num(s)
		return n, n, ok
	}
	lo, ok1 := num(s[:dash])
	rest := s[dash+1:]
	if rest == "" {
		return lo, 1000, ok1
	}
	hi, ok2 := num(rest)
	if !ok1 || !ok2 || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

type mPlan struct {
	version  int
	steps    []int
	reqLossy int
	resLossy int
	warning  int64
	regVer   uint64
}

type mErr struct {
	kind Kind
	step int
}

const kindOK = Kind(-1)

// check 按 预览 > 下线 > 路径 > 有损 的次序检查候选 v。
func (m *model) check(v int, allowLossy bool, scopes map[string]bool, now int64) (Kind, int) {
	if sc, ok := m.previews[v]; ok && !scopes[sc] {
		return KindNoPermission, 0
	}
	for u := v; u < m.h; u++ {
		if t, ok := m.sunsets[u]; ok && now >= t {
			return KindSunset, 0
		}
	}
	for u := v; u < m.h; u++ {
		if _, ok := m.steps[u]; !ok {
			return KindNoPath, u
		}
	}
	if !allowLossy {
		for u := v; u < m.h; u++ {
			if s, ok := m.steps[u]; ok && s[0] {
				return KindLossy, u
			}
		}
	}
	return kindOK, 0
}

func kindName(k Kind) string {
	switch k {
	case KindInvalidArgument:
		return "参数非法"
	case KindSyntax:
		return "语法错误"
	case KindInvalidTime:
		return "时间非法"
	case KindFutureVersion:
		return "未来版本"
	case KindNoPermission:
		return "无权限"
	case KindSunset:
		return "已下线"
	case KindNoPath:
		return "无路径"
	case KindLossy:
		return "有损"
	}
	return "?"
}

func (m *model) negotiate(rng string, allowLossy bool, scopes []string, now int64) (mPlan, *mErr, string) {
	for _, s := range scopes {
		if s == "" {
			return mPlan{}, &mErr{KindInvalidArgument, 0}, "scopes 含空串"
		}
	}
	lo, hi, ok := parseRange(rng)
	if !ok {
		return mPlan{}, &mErr{KindSyntax, 0}, "区间语法错误"
	}
	if now < 0 || now > adapter.MaxTime {
		return mPlan{}, &mErr{KindInvalidTime, 0}, "now 越界"
	}
	if lo > m.h {
		return mPlan{}, &mErr{KindFutureVersion, 0}, fmt.Sprintf("lo=%d > H=%d", lo, m.h)
	}
	top := hi
	if top > m.h {
		top = m.h
	}
	scopeSet := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		scopeSet[s] = true
	}
	var why strings.Builder
	var topErr *mErr
	for v := top; v >= lo; v-- {
		kind, step := m.check(v, allowLossy, scopeSet, now)
		if kind == kindOK {
			p := mPlan{version: v, steps: []int{}, warning: -1, regVer: m.ver}
			for u := v; u < m.h; u++ {
				p.steps = append(p.steps, u)
				s := m.steps[u]
				if s[0] {
					p.reqLossy++
				}
				if s[1] {
					p.resLossy++
				}
				if t, ok := m.sunsets[u]; ok {
					if rem := t - now; p.warning < 0 || rem < p.warning {
						p.warning = rem
					}
				}
			}
			fmt.Fprintf(&why, "候选 %d 可服务，胜出", v)
			return p, nil, why.String()
		}
		if v == top {
			topErr = &mErr{kind, step}
		}
		fmt.Fprintf(&why, "候选 %d: %s(步 %d); ", v, kindName(kind), step)
	}
	why.WriteString("全部不可服务，报 top 的原因")
	return mPlan{}, topErr, why.String()
}

func randVersion(rng *rand.Rand, h int) int {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return h + 1
	default:
		return 1 + rng.Intn(h+1)
	}
}

func randTime(rng *rand.Rand) int64 {
	switch rng.Intn(12) {
	case 0:
		return -1
	case 1:
		return adapter.MaxTime
	case 2:
		return adapter.MaxTime + 1
	default:
		return int64(rng.Intn(200))
	}
}

func randScope(rng *rand.Rand) string {
	switch rng.Intn(6) {
	case 0:
		return ""
	case 1, 2:
		return "a"
	default:
		return "b"
	}
}

func randRange(rng *rand.Rand) string {
	n := func() int { return 1 + rng.Intn(8) }
	switch rng.Intn(14) {
	case 0:
		return ""
	case 1:
		return "0"
	case 2:
		return fmt.Sprintf("%02d", n())
	case 3:
		return fmt.Sprintf("%d-%d", n()+2, n())
	case 4:
		return "1-2-3"
	case 5:
		return "x"
	case 6:
		return fmt.Sprintf("%d", 1000+rng.Intn(10))
	case 7, 8:
		return fmt.Sprintf("%d-", n())
	case 9, 10, 11:
		lo, hi := n(), n()
		if lo > hi {
			lo, hi = hi, lo
		}
		return fmt.Sprintf("%d-%d", lo, hi)
	default:
		return fmt.Sprintf("%d", n())
	}
}

func randScopes(rng *rand.Rand) []string {
	var out []string
	for _, s := range []string{"a", "b"} {
		if rng.Intn(2) == 0 {
			out = append(out, s)
		}
	}
	if rng.Intn(20) == 0 {
		out = append(out, "")
	}
	return out
}

func realMutKind(err error) mutResult {
	if err == nil {
		return mutOK
	}
	if aerr, ok := err.(*adapter.Error); ok && aerr.Kind == adapter.KindNotFound {
		return mutNotFound
	}
	return mutInvalid
}

func TestRandomReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < 2000; seq++ {
		h := 1 + rng.Intn(6)
		reg, err := adapter.New(h)
		if err != nil {
			t.Fatalf("seq %d: New(%d) err = %v", seq, h, err)
		}
		neg := New(reg)
		m := newModel(h)
		nOps := 1 + rng.Intn(24)
		t.Logf("seq %d: H=%d, %d ops", seq, h, nOps)
		for op := 0; op < nOps; op++ {
			where := fmt.Sprintf("seq %d op %d", seq, op)
			switch rng.Intn(10) {
			case 0, 1, 2: // SetAdapter
				v, req, resp := randVersion(rng, h), rng.Intn(2) == 0, rng.Intn(2) == 0
				got := realMutKind(reg.SetAdapter(v, req, resp))
				want := m.setAdapter(v, req, resp)
				t.Logf("%s: SetAdapter(v=%d req=%v resp=%v) -> %v, ver=%d", where, v, req, resp, got, m.ver)
				if got != want {
					t.Fatalf("%s: SetAdapter kind = %v, model %v", where, got, want)
				}
			case 3: // RemoveAdapter
				v := randVersion(rng, h)
				got := realMutKind(reg.RemoveAdapter(v))
				want := m.removeAdapter(v)
				t.Logf("%s: RemoveAdapter(v=%d) -> %v, ver=%d", where, v, got, m.ver)
				if got != want {
					t.Fatalf("%s: RemoveAdapter kind = %v, model %v", where, got, want)
				}
			case 4, 5: // Sunset
				v, ts := randVersion(rng, h), randTime(rng)
				got := realMutKind(reg.Sunset(v, ts))
				want := m.sunset(v, ts)
				t.Logf("%s: Sunset(v=%d t=%d) -> %v, ver=%d", where, v, ts, got, m.ver)
				if got != want {
					t.Fatalf("%s: Sunset kind = %v, model %v", where, got, want)
				}
			case 6: // Preview
				v, sc := randVersion(rng, h), randScope(rng)
				got := realMutKind(reg.Preview(v, sc))
				want := m.preview(v, sc)
				t.Logf("%s: Preview(v=%d scope=%q) -> %v, ver=%d", where, v, sc, got, m.ver)
				if got != want {
					t.Fatalf("%s: Preview kind = %v, model %v", where, got, want)
				}
			default: // Negotiate
				rangeStr, allowLossy := randRange(rng), rng.Intn(2) == 0
				scopes, now := randScopes(rng), randTime(rng)
				plan, err := neg.Negotiate(rangeStr, allowLossy, scopes, now)
				mp, merr, why := m.negotiate(rangeStr, allowLossy, scopes, now)
				if merr == nil {
					t.Logf("%s: Negotiate(%q allowLossy=%v scopes=%v now=%d) -> v=%d steps=%v req=%d resp=%d warn=%d regVer=%d | 依据: %s",
						where, rangeStr, allowLossy, scopes, now, mp.version, mp.steps, mp.reqLossy, mp.resLossy, mp.warning, mp.regVer, why)
					if err != nil {
						t.Fatalf("%s: Negotiate err = %v, model plan %+v", where, err, mp)
					}
					want := Plan{Version: mp.version, Steps: mp.steps, ReqLossySteps: mp.reqLossy,
						RespLossySteps: mp.resLossy, Warning: mp.warning, RegistryVersion: mp.regVer}
					if fmt.Sprintf("%+v", plan) != fmt.Sprintf("%+v", want) {
						t.Fatalf("%s: Negotiate = %+v, model %+v", where, plan, want)
					}
				} else {
					t.Logf("%s: Negotiate(%q allowLossy=%v scopes=%v now=%d) -> %s(步 %d) | 依据: %s",
						where, rangeStr, allowLossy, scopes, now, kindName(merr.kind), merr.step, why)
					nerr, ok := err.(*Error)
					if !ok {
						t.Fatalf("%s: Negotiate err = %v, model %s(步 %d)", where, err, kindName(merr.kind), merr.step)
					}
					if nerr.Kind != merr.kind || nerr.Step != merr.step {
						t.Fatalf("%s: Negotiate = %s(步 %d), model %s(步 %d)",
							where, kindName(nerr.Kind), nerr.Step, kindName(merr.kind), merr.step)
					}
				}
			}
			if got := reg.Version(); got != m.ver {
				t.Fatalf("%s: registry version = %d, model %d", where, got, m.ver)
			}
		}
	}
}
