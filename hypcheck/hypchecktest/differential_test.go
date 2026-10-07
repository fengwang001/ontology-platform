package hypchecktest

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/hypcheck"
)

// canonicalSig 把裁决压成与实现细节无关的稳定签名，供两个模型逐条对照。
func canonicalSig(r *hypcheck.PrecheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict=%s", r.Verdict)
	if r.Verdict == hypcheck.VerdictError {
		fmt.Fprintf(&b, "|err=%s", r.ErrorClass)
	}
	fmt.Fprintf(&b, "|type=%s", r.TypeVersion)
	hookIDs := append([]string(nil), hookSig(r.Hooks)...)
	sort.Strings(hookIDs)
	b.WriteString("|hooks=" + strings.Join(hookIDs, ","))
	pre := failureSig(r.PreFailures)
	post := failureSig(r.PostFailures)
	b.WriteString("|pre=" + strings.Join(pre, ","))
	b.WriteString("|post=" + strings.Join(post, ","))
	if r.PermTrace != nil {
		fmt.Fprintf(&b, "|perm=%t/%s", r.PermTrace.Allowed, r.PermTrace.GrantedBy)
	}
	var effs []string
	for _, ef := range r.Effects {
		effs = append(effs, ef.Key+"="+valueSig(ef.Value))
	}
	sort.Strings(effs)
	b.WriteString("|eff=" + strings.Join(effs, ","))
	return b.String()
}

func hookSig(hs []hypcheck.ResolvedHook) []string {
	var out []string
	for _, h := range hs {
		out = append(out, string(h.Phase)+":"+h.HookID+"@"+h.Version)
	}
	return out
}

func failureSig(fs []hypcheck.PhaseFailure) []string {
	var out []string
	for _, f := range fs {
		out = append(out, string(f.Phase)+":"+string(f.Code)+":"+f.HookID)
	}
	sort.Strings(out)
	return out
}

func valueSig(v hypcheck.Value) string {
	switch v.Kind {
	case hypcheck.KindInt:
		return fmt.Sprintf("i%d", v.Int)
	case hypcheck.KindStr:
		return "s" + v.Str
	case hypcheck.KindBool:
		return fmt.Sprintf("b%t", v.Bool)
	default:
		return "?"
	}
}

// TestRandomDifferential 随机生成大量真实操作与假设预检，逐条对照引擎与朴素重演。
func TestRandomDifferential(t *testing.T) {
	const runs = 40
	for seed := int64(1); seed <= runs; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			e := hypcheck.NewEngine(hypcheck.Config{})
			naive := hypcheck.NewNaiveReplay()

			typeIDs := []string{"A", "B"}
			users := []string{"u1", "u2", "u3"}
			orgs := []string{"o1", "o2"}
			hookIDs := []string{"h1", "h2", "h3", "h4"}
			keys := []string{"k1", "k2", "s1", "s2"}

			// 初始世界：t=0 定义类型（不同 schema）。
			for _, ty := range typeIDs {
				tv := hypcheck.TypeVersion{
					Schema: hypcheck.Schema{Fields: []hypcheck.Field{
						{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: rng.Intn(2) == 0},
						{Name: "n", Type: hypcheck.FieldType(hypcheck.KindInt), Required: false},
					}},
					Effects: []hypcheck.EffectDecl{{Key: "k1", Param: "p"}},
				}
				must(t, e.DefineType(0, ty, tv))
			}
			for _, u := range users {
				must(t, e.UpsertPrincipal(0, u, true))
			}
			for _, o := range orgs {
				must(t, e.UpsertPrincipal(0, o, true))
			}
			// 预置已发布钩子，含前置/后置各两种。
			hookSpecs := []hypcheck.HookSpec{
				{Kind: hypcheck.PreDenyParamEqual, Param: "p", Value: hypcheck.StrValue("x")},
				{Kind: hypcheck.PreDenyStateEqual, Key: "s1", Value: hypcheck.StrValue("locked")},
				{Kind: hypcheck.PostDenyStateEqual, Key: "s2", Value: hypcheck.StrValue("block")},
				{Kind: hypcheck.PostDenyIntendedSet, Key: "k1", Value: hypcheck.StrValue("z")},
			}
			for i, h := range hookIDs {
				phase := hypcheck.PhasePre
				if hookSpecs[i].Kind == hypcheck.PostDenyStateEqual || hookSpecs[i].Kind == hypcheck.PostDenyIntendedSet {
					phase = hypcheck.PhasePost
				}
				must(t, e.PublishHook(0, h, phase, hypcheck.HookVersion{Version: "v1", Spec: hookSpecs[i]}))
			}

			now := hypcheck.Timestamp(0)
			advance := func() hypcheck.Timestamp {
				now += hypcheck.Timestamp(1 + rng.Intn(3))
				return now
			}

			const ops = 400
			for op := 0; op < ops; op++ {
				at := advance()
				switch rng.Intn(8) {
				case 0: // 重新定义类型（schema/effect 版本演进）
					ty := typeIDs[rng.Intn(len(typeIDs))]
					required := rng.Intn(2) == 0
					_ = e.DefineType(at, ty, hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
						{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: required}},
					}, Effects: []hypcheck.EffectDecl{{Key: keys[rng.Intn(len(keys))], Param: "p"}}})
				case 1: // 钩子替换为新版本（同 kind，边界两侧不同拒绝值）
					h := hookIDs[rng.Intn(len(hookIDs))]
					spec := hookSpecs[indexOf(h, hookIDs)]
					if rng.Intn(2) == 0 {
						spec.Value = hypcheck.StrValue("y")
					}
					phase := hypcheck.PhasePre
					if spec.Kind == hypcheck.PostDenyStateEqual || spec.Kind == hypcheck.PostDenyIntendedSet {
						phase = hypcheck.PhasePost
					}
					_ = e.PublishHook(at, h, phase, hypcheck.HookVersion{
						Version: fmt.Sprintf("v%d", at), Spec: spec})
				case 2, 3: // 绑定 / 解绑
					ty := typeIDs[rng.Intn(len(typeIDs))]
					h := hookIDs[rng.Intn(len(hookIDs))]
					spec := hookSpecs[indexOf(h, hookIDs)]
					phase := hypcheck.PhasePre
					if spec.Kind == hypcheck.PostDenyStateEqual || spec.Kind == hypcheck.PostDenyIntendedSet {
						phase = hypcheck.PhasePost
					}
					if rng.Intn(3) == 0 {
						_ = e.UnbindHook(at, ty, h)
					} else {
						_ = e.BindHook(at, ty, h, phase)
					}
				case 4: // 继承边调整
					u := users[rng.Intn(len(users))]
					o := orgs[rng.Intn(len(orgs))]
					_ = e.ToggleEdge(at, o, u, rng.Intn(2) == 0)
				case 5: // 直接授权调整
					var node string
					if rng.Intn(2) == 0 {
						node = orgs[rng.Intn(len(orgs))]
					} else {
						node = users[rng.Intn(len(users))]
					}
					_ = e.ToggleGrant(at, node, typeIDs[rng.Intn(len(typeIDs))], rng.Intn(2) == 0)
				case 6: // 状态写入
					_ = e.WriteState(at, keys[rng.Intn(len(keys))], hypcheck.StrValue([]string{"x", "y", "z", "locked", "block"}[rng.Intn(5)]))
				case 7: // 身份注销 / 恢复
					u := users[rng.Intn(len(users))]
					_ = e.UpsertPrincipal(at, u, rng.Intn(2) == 0)
				}

				// 每若干次操作，在随机历史时刻发起假设预检并对照。
				if op%3 == 0 {
					var at2 hypcheck.Timestamp
					if rng.Intn(2) == 0 && now > 0 {
						at2 = hypcheck.Timestamp(rng.Int63n(int64(now) + 1)) // 含边界
					} else {
						at2 = at
					}
					req := hypcheck.PrecheckRequest{
						At:         at2,
						TypeID:     typeIDs[rng.Intn(len(typeIDs))],
						Caller:     users[rng.Intn(len(users))],
						CollectAll: rng.Intn(2) == 0,
						ObjectKeys: keys,
					}
					p := []string{"x", "y", "z", ""}[rng.Intn(4)]
					req.Params = hypcheck.Params{}
					if p != "" {
						req.Params["p"] = hypcheck.StrValue(p)
					}
					if p == "" || rng.Intn(2) == 0 {
						req.Params["n"] = hypcheck.IntValue(rng.Int63n(5))
					}

					naive.LoadEvents(e.JournalEvents(), e.Manifest())
					got := e.Precheck(req)
					want := naive.Precheck(req)
					if canonicalSig(got) != canonicalSig(want) {
						t.Fatalf("seed=%d op=%d at=%d mismatch\n req=%+v\n engine=%s\n naive =%s",
							seed, op, at2, req, canonicalSig(got), canonicalSig(want))
					}
				}
			}
		})
	}
}

func indexOf(s string, all []string) int {
	for i, v := range all {
		if v == s {
			return i
		}
	}
	return 0
}
