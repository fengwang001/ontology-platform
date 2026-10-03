package negotiate

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/adapter"
)

// refStep 是朴素模拟器里的适配步状态。
type refStep struct {
	present   bool
	reqLossy  bool
	respLossy bool
}

// naiveRegistry 完全按题目规则独立书写的朴素模拟。
type naiveRegistry struct {
	h       int
	rev     int
	steps   map[int]refStep
	sunset  map[int]int64
	preview map[int]string
}

func newNaive(h int) *naiveRegistry {
	return &naiveRegistry{
		h:       h,
		steps:   map[int]refStep{},
		sunset:  map[int]int64{},
		preview: map[int]string{},
	}
}

func refErrOK(err error) bool { return err != nil }

// refResult 是朴素协商的结果：失败类别/步号 或 成功计划。
type refResult struct {
	ok     bool
	reason Reason
	step   int
	plan   Plan
}

func (m *naiveRegistry) negotiate(text string, allowLossy bool, scopes []string, now int64) refResult {
	for _, s := range scopes {
		if s == "" {
			return refResult{reason: ReasonInvalidArgument}
		}
	}

	lo, hi, syntaxOK := refParse(text)
	if !syntaxOK {
		return refResult{reason: ReasonSyntax}
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return refResult{reason: ReasonInvalidTime}
	}
	if lo > m.h {
		return refResult{reason: ReasonFuture}
	}
	top := hi
	if top > m.h {
		top = m.h
	}
	hold := map[string]bool{}
	for _, s := range scopes {
		hold[s] = true
	}

	topReason := Reason(0)
	topStep := 0
	for v := top; v >= lo; v-- {
		reason, step := m.evaluate(v, allowLossy, hold, now)
		if v == top {
			topReason, topStep = reason, step
		}
		if reason == 0 {
			p := Plan{Version: v, Steps: []int{}, Warning: -1, RegistryRev: m.rev}
			for x := v; x < m.h; x++ {
				p.Steps = append(p.Steps, x)
				s := m.steps[x]
				if s.reqLossy {
					p.ReqLossy++
				}
				if s.respLossy {
					p.RespLossy++
				}
				if t, registered := m.sunset[x]; registered {
					d := t - now
					if p.Warning == -1 || d < p.Warning {
						p.Warning = d
					}
				}
			}
			return refResult{ok: true, plan: p}
		}
	}
	return refResult{reason: topReason, step: topStep}
}

// evaluate 严格按次序取候选 v 的第一个不可服务原因。
func (m *naiveRegistry) evaluate(v int, allowLossy bool, scopes map[string]bool, now int64) (Reason, int) {
	if scope, previewed := m.preview[v]; previewed && !scopes[scope] {
		return ReasonNoPermission, 0
	}
	for x := v; x < m.h; x++ {
		if t, ok := m.sunset[x]; ok && now >= t {
			return ReasonSunset, 0
		}
	}
	for x := v; x < m.h; x++ {
		if !m.steps[x].present {
			return ReasonNoPath, x
		}
	}
	if !allowLossy {
		for x := v; x < m.h; x++ {
			if m.steps[x].reqLossy {
				return ReasonLossy, x
			}
		}
	}
	return 0, 0
}

// refParse 是独立实现的区间文法：返回 lo、hi 与是否合法。
func refParse(s string) (int, int, bool) {
	isDigits := func(p string) bool {
		if len(p) == 0 {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
		return true
	}
	num := func(p string) (int, bool) {
		if !isDigits(p) || (len(p) > 1 && p[0] == '0') {
			return 0, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 1000 {
			return 0, false
		}
		return n, true
	}
	dash := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			if dash != -1 {
				return 0, 0, false
			}
			dash = i
		}
	}
	if dash == -1 {
		n, ok := num(s)
		return n, n, ok
	}
	lo, ok := num(s[:dash])
	if !ok {
		return 0, 0, false
	}
	tail := s[dash+1:]
	if tail == "" {
		return lo, 1000, true
	}
	hi, ok := num(tail)
	if !ok || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

// op 是重放日志中的一步操作。
type op struct {
	kind      string
	v         int
	t         int64
	scope     string
	req       bool
	resp      bool
	rangeText string
	lossy     bool
	scopes    []string
	now       int64
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261003))

	for seq := 0; seq < sequences; seq++ {
		h := 1 + rng.Intn(8)
		realReg, err := adapter.New(h)
		if err != nil {
			t.Fatal(err)
		}
		real := New(realReg)
		model := newNaive(h)
		log := []op{}

		scopesPool := []string{"alpha", "beta", "gamma"}
		mutCount := 3 + rng.Intn(3*h+4)
		for i := 0; i < mutCount; i++ {
			switch rng.Intn(6) {
			case 0, 1:
				v := 1 + rng.Intn(h)
				req := rng.Intn(2) == 0
				resp := rng.Intn(2) == 0
				log = append(log, op{kind: "set", v: v, req: req, resp: resp})
				gotErr := realReg.SetAdapter(v, req, resp)
				if v < h {
					model.steps[v] = refStep{present: true, reqLossy: req, respLossy: resp}
					model.rev++
				}
				modelCheckErr(t, seq, gotErr, v >= h, log)
			case 2:
				v := 1 + rng.Intn(h)
				log = append(log, op{kind: "remove", v: v})
				gotErr := realReg.RemoveAdapter(v)
				_, existed := model.steps[v]
				if v < h && existed {
					delete(model.steps, v)
					model.rev++
				}
				modelCheckErr(t, seq, gotErr, v >= h || !existed, log)
			case 3:
				v := 1 + rng.Intn(h)
				var tt int64
				if rng.Intn(8) == 0 {
					tt = int64(1_000_000_000_000_001)
				} else {
					tt = rng.Int63n(200)
				}
				log = append(log, op{kind: "sunset", v: v, t: tt})
				gotErr := realReg.Sunset(v, tt)
				if v < h && tt >= 0 && tt <= 1_000_000_000_000_000 {
					model.sunset[v] = tt
					model.rev++
				}
				modelCheckErr(t, seq, gotErr, v >= h || tt < 0 || tt > 1_000_000_000_000_000, log)
			case 4:
				v := 1 + rng.Intn(h+1)
				scope := scopesPool[rng.Intn(len(scopesPool))]
				if rng.Intn(10) == 0 {
					scope = ""
				}
				log = append(log, op{kind: "preview", v: v, scope: scope})
				gotErr := realReg.Preview(v, scope)
				if v >= 1 && v <= h && scope != "" {
					model.preview[v] = scope
					model.rev++
				}
				modelCheckErr(t, seq, gotErr, v < 1 || v > h || scope == "", log)
			case 5:
				var text string
				switch rng.Intn(6) {
				case 0:
					text = strconv.Itoa(1 + rng.Intn(1000))
				case 1:
					text = strconv.Itoa(1+rng.Intn(500)) + "-" + strconv.Itoa(500+rng.Intn(501))
				case 2:
					text = strconv.Itoa(1+rng.Intn(1000)) + "-"
				case 3:
					text = "0" + strconv.Itoa(1+rng.Intn(9))
				case 4:
					text = strconv.Itoa(1+rng.Intn(3)) + "-" + strconv.Itoa(rng.Intn(3))
				default:
					text = []string{"", "0", "1001", "1-1001", "a-", "-3", "1--"}[rng.Intn(7)]
				}
				lossy := rng.Intn(2) == 0
				now := rng.Int63n(200)
				if rng.Intn(12) == 0 {
					now = -1
				}
				var scopes []string
				nScope := rng.Intn(3)
				for j := 0; j < nScope; j++ {
					s := scopesPool[rng.Intn(len(scopesPool))]
					if rng.Intn(15) == 0 {
						s = ""
					}
					scopes = append(scopes, s)
				}
				log = append(log, op{kind: "negotiate", rangeText: text, lossy: lossy, scopes: scopes, now: now})

				gotPlan, gotErr := real.Negotiate(text, lossy, scopes, now)
				want := model.negotiate(text, lossy, scopes, now)
				if want.ok {
					if gotErr != nil {
						t.Fatalf("seq=%d 朴素成功但实现失败 %v\n操作序列=%v", seq, gotErr, log)
					}
					normalize(&gotPlan)
					if !reflect.DeepEqual(gotPlan, want.plan) {
						t.Fatalf("seq=%d 计划不一致\n实现=%+v\n朴素=%+v\n操作序列=%v", seq, gotPlan, want.plan, log)
					}
				} else {
					if gotErr == nil {
						t.Fatalf("seq=%d 朴素失败(%s) 但实现成功 %+v\n操作序列=%v",
							seq, want.reason, gotPlan, log)
					}
					var ne *Error
					if !asError(gotErr, &ne) || ne.Reason != want.reason || ne.Step != want.step {
						t.Fatalf("seq=%d 错误不一致：实现=%v 朴素=%s(步%d)\n操作序列=%v",
							seq, gotErr, want.reason, want.step, log)
					}
				}
				if realReg.Revision() != model.rev {
					t.Fatalf("seq=%d 注册表版本号分叉：实现=%d 朴素=%d", seq, realReg.Revision(), model.rev)
				}
			}
		}
		if seq < 5 || seq == sequences-1 {
			t.Logf("seq=%d h=%d ops=%d 终态rev=%d 与朴素模型完全一致", seq, h, len(log), model.rev)
		}
	}
}

func normalize(p *Plan) {
	if p.Steps == nil {
		p.Steps = []int{}
	}
}

func modelCheckErr(t *testing.T, seq int, gotErr error, wantErr bool, log []op) {
	t.Helper()
	if wantErr != refErrOK(gotErr) {
		t.Fatalf("seq=%d 变更拒绝状态不一致：gotErr=%v wantErr=%v\n操作序列=%v",
			seq, gotErr, wantErr, log)
	}
}
