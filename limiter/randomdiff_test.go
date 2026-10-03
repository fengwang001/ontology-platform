package limiter

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"

	"ontology/rule"
)

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errCat(err error) string {
	switch {
	case err == nil:
		return ""
	case err == ErrInvalid:
		return "invalid"
	case err == ErrExists:
		return "exists"
	case err == ErrDuplicate:
		return "duplicate"
	case err == ErrNotFound:
		return "notfound"
	case err == ErrTime:
		return "time"
	case err == ErrClockGoBack:
		return "back"
	default:
		return "?"
	}
}

func realState(l *Limiter) (map[string][3]int64, map[string]int64, int) {
	cs := map[string][3]int64{}
	for sig, s := range dumpCounters(l) {
		cs[sig] = [3]int64{s.K, s.Cur, s.Prev}
	}
	sh := shadowSnapshot(l)
	return cs, sh, l.Tracked()
}

func TestRandomDifferential(t *testing.T) {
	verbose := os.Getenv("VERBOSE_DIFF") != ""
	const seqN = 2000
	const opN = 240
	keys := []string{"tenant", "route", "zone", "user"}
	vals := []string{"*", "a", "b", "/pay", "/login", ""}

	for seq := 0; seq < seqN; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 77))
		l := New()
		m := newNaive()
		var log []string

		type ruleInfo struct {
			id  string
			sig string
		}
		var rules []ruleInfo
		now := int64(0)
		everShadow := map[string]bool{}

		failf := func(format string, args ...any) {
			t.Helper()
			for _, line := range log {
				t.Log(line)
			}
			t.Fatalf("seq=%d %s", seq, fmt.Sprintf(format, args...))
		}

		for op := 0; op < opN; op++ {
			kind := rng.Intn(100)
			switch {
			case kind < 42: // AddRule
				id := "r" + strconv.Itoa(rng.Intn(12))
				pn := 1 + rng.Intn(3)
				used := map[string]bool{}
				var pat []rule.Pair
				for pn > len(pat) {
					k := keys[rng.Intn(len(keys))]
					if used[k] {
						continue
					}
					used[k] = true
					v := vals[rng.Intn(len(vals))]
					pat = append(pat, rule.Pair{Key: k, Value: v})
				}
				lim := int64(1 + rng.Intn(4))
				w := int64(1 + rng.Intn(300))
				mode := rule.Mode(rng.Intn(2))
				// 小概率制造非法参数。
				if rng.Intn(15) == 0 {
					pat[0].Value = ""
				}
				e1 := l.AddRule(id, pat, lim, w, mode)
				e2 := m.add(id, pat, lim, w, mode)
				log = append(log, fmt.Sprintf("AddRule id=%s pat=%v L=%d W=%d mode=%d => real=%v naive=%v",
					id, pat, lim, w, mode, errCat(e1), errstr(e2)))
				if errCat(e1) != errstr(e2) {
					failf("AddRule mismatch: real=%v naive=%v", errCat(e1), errstr(e2))
				}
				if e1 == nil {
					rules = append(rules, ruleInfo{id: id, sig: nSig(pat)})
					if mode == rule.Shadow {
						everShadow[id] = true
					}
				}

			case kind < 50 && len(rules) > 0: // SetMode
				ri := rules[rng.Intn(len(rules))]
				mode := rule.Mode(rng.Intn(2))
				e1 := l.SetMode(ri.id, mode)
				e2 := m.setMode(ri.id, mode)
				log = append(log, fmt.Sprintf("SetMode %s mode=%d => real=%v naive=%v",
					ri.id, mode, errCat(e1), errstr(e2)))
				if errCat(e1) != errstr(e2) {
					failf("SetMode mismatch")
				}
				if e1 == nil && mode == rule.Shadow {
					everShadow[ri.id] = true
				}

			case kind < 56 && len(rules) > 0: // RemoveRule
				ri := rules[rng.Intn(len(rules))]
				e1 := l.RemoveRule(ri.id)
				e2 := m.remove(ri.id)
				log = append(log, fmt.Sprintf("RemoveRule %s => real=%v naive=%v",
					ri.id, errCat(e1), errstr(e2)))
				if errCat(e1) != errstr(e2) {
					failf("RemoveRule mismatch")
				}
				out := rules[:0]
				for _, x := range rules {
					if x.id != ri.id {
						out = append(out, x)
					}
				}
				rules = out
				if e1 == nil {
					delete(everShadow, ri.id)
				}

			default: // Allow
				// 时间：多数单调前进，少量回退/越界触发拒绝路径。
				switch rng.Intn(12) {
				case 0:
					now = rng.Int63n(now + 2) // 可能回退
				case 1:
					now += 1 + rng.Int63n(400)
				default:
					if rng.Intn(3) == 0 {
						now += int64(rng.Intn(250))
					}
				}
				if rng.Intn(40) == 0 {
					now = 1_000_000_000_000_001 // 越界
				}
				desc := rule.Descriptor{}
				dn := 1 + rng.Intn(4)
				dused := map[string]bool{}
				for len(desc) < dn {
					k := keys[rng.Intn(len(keys))]
					if dused[k] {
						continue
					}
					dused[k] = true
					pool := []string{"a", "b", "/pay", "/login", "vip"}
					desc[k] = pool[rng.Intn(len(pool))]
				}
				if rng.Intn(30) == 0 {
					desc["bad"] = "" // 非法值
				}
				d1, e1 := l.Allow(desc, now)
				d2, e2 := m.allow(desc, now)
				log = append(log, fmt.Sprintf(
					"Allow desc=%v now=%d => real{ok=%v by=%q sel=%v sh=%v err=%v} naive{ok=%v by=%q sel=%v sh=%v err=%v}",
					desc, now,
					d1.Allowed, d1.RejectedBy, d1.Selected, d1.ShadowReject, errCat(e1),
					d2.allowed, d2.rejectedBy, d2.selected, d2.shadowReject, errstr(e2)))
				if errCat(e1) != errstr(e2) {
					failf("Allow err mismatch")
				}
				if e1 == nil {
					if d1.Allowed != d2.allowed || d1.RejectedBy != d2.rejectedBy ||
						!eqStrings(d1.Selected, d2.selected) ||
						!eqStrings(d1.ShadowReject, d2.shadowReject) {
						failf("Allow decision mismatch:\n real=%+v\nnaive=%+v", d1, d2)
					}
				}
			}

			// 每一步比对全部内部状态。
			rc, rsh, rn := realState(l)
			nc, nsh, nn := m.state()
			if rn != nn {
				failf("Tracked mismatch real=%d naive=%d", rn, nn)
			}
			if !mapEq3(rc, nc) {
				failf("counter state mismatch:\n real=%v\nnaive=%v", rc, nc)
			}
			if !mapEqI64(rsh, nsh) {
				failf("shadow stats mismatch:\n real=%v\nnaive=%v", rsh, nsh)
			}
			// 跨包不变量：cur/prev<=L+1；从未影子过的规则 <=L。
			for sig, c := range rc {
				id := sig
				if i := indexByte(sig, 0); i >= 0 {
					id = sig[:i]
				}
				r, ok := l.rules[id]
				if !ok {
					failf("counter for missing rule %q", id)
				}
				cap := r.L
				if everShadow[id] {
					cap = r.L + 1
				}
				if c[1] > cap || c[2] > cap {
					failf("invariant broken for %s: k=%d cur=%d prev=%d cap=%d",
						id, c[0], c[1], c[2], cap)
				}
			}
			if verbose || seq < 3 {
				t.Logf("[seq=%d op=%d] %s", seq, op, log[len(log)-1])
			}
		}
		if seq < 3 {
			t.Logf("seq=%d 完成，%d 个操作，Tracked=%d（输入/输出/判定见上方日志）", seq, opN, l.Tracked())
		}
	}
}

func errstr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func mapEq3(a, b map[string][3]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mapEqI64(a, b map[string]int64) bool {
	ka := make([]string, 0, len(a))
	for k, v := range a {
		if v != 0 {
			ka = append(ka, k)
		}
	}
	kb := make([]string, 0, len(b))
	for k, v := range b {
		if v != 0 {
			kb = append(kb, k)
		}
	}
	sort.Strings(ka)
	sort.Strings(kb)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] || a[ka[i]] != b[kb[i]] {
			return false
		}
	}
	return true
}

// 并发冒烟：所有方法可被并发调用，串行化语义由锁保证，-race 下应无告警。
func TestConcurrentSmoke(t *testing.T) {
	l := New()
	if err := l.AddRule("r", []rule.Pair{{Key: "route", Value: "*"}}, 50, 100, rule.Enforce); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// 全部使用同一时刻，避免回退错误。
				_, _ = l.Allow(rule.Descriptor{"route": fmt.Sprintf("u%d", g%4)}, 5)
				_ = l.Tracked()
			}
		}(g)
	}
	wg.Wait()
}
