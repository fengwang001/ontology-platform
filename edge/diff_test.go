package edge_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/edge"
	"ontology/purge"
	"ontology/quota"
)

// naiveModel 用逐条前缀比较 + 独立记账模拟规格，作为差分对照。
type naiveModel struct {
	limits map[string]quota.Limits
	day    map[string]int64
	used   map[string][3]int
	rules  map[string]map[string]int64 // raw path -> epoch（URL 与目录混存）
	cache  map[string]map[string]int64
	queue  []qpair
	inQ    map[string]map[string]struct{}
	epoch  int64
	maxNow int64
	lmax   int
}

type qpair struct{ tenant, url string }

func newNaive(lmax int) *naiveModel {
	return &naiveModel{
		limits: map[string]quota.Limits{},
		day:    map[string]int64{},
		used:   map[string][3]int{},
		rules:  map[string]map[string]int64{},
		cache:  map[string]map[string]int64{},
		inQ:    map[string]map[string]struct{}{},
		lmax:   lmax,
	}
}

func (m *naiveModel) register(name string, l quota.Limits) { m.limits[name] = l }

func segments(raw string) []string {
	if raw == "/" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(raw, "/")[1:], "/")
}

// naiveCovers 目录 dir 覆盖 URL u 的朴素逐条判定。
func naiveCovers(dir, url string) bool {
	dp, up := segments(dir), segments(url)
	if len(dp) == 0 {
		return len(up) > 0
	}
	if len(up) <= len(dp) {
		return false
	}
	for i := range dp {
		if up[i] != dp[i] {
			return false
		}
	}
	return true
}

func (m *naiveModel) roll(tenant string, now int64) {
	day := now / 86400
	if m.day[tenant] != day {
		m.day[tenant] = day
		m.used[tenant] = [3]int{}
	}
}

func (m *naiveModel) maxEpoch(tenant, url string) int64 {
	var maxE int64
	for raw, e := range m.rules[tenant] {
		if raw == url || (strings.HasSuffix(raw, "/") && naiveCovers(raw, url)) {
			if e > maxE {
				maxE = e
			}
		}
	}
	return maxE
}

func (m *naiveModel) fresh(tenant, url string) edge.FreshStatus {
	filled, ok := m.cache[tenant][url]
	if !ok {
		return edge.StatusMissing
	}
	if filled >= m.maxEpoch(tenant, url) {
		return edge.StatusFresh
	}
	return edge.StatusStale
}

// normalize 朴素规范化：去重 + 逐项查是否被同批某目录覆盖。
func (m *naiveModel) normalize(items []string) (kept []string, u, d int, ok bool) {
	uniq := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		if _, err := purge.Parse(it); err != nil {
			return nil, 0, 0, false
		}
		if !seen[it] {
			seen[it] = true
			uniq = append(uniq, it)
		}
	}
	for _, cand := range uniq {
		covered := false
		probe := cand
		if strings.HasSuffix(cand, "/") {
			probe = strings.TrimSuffix(cand, "/") + "/x" // 用一个子 URL 测目录覆盖
		}
		for _, other := range uniq {
			if other != cand && strings.HasSuffix(other, "/") && naiveCovers(other, probe) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept = append(kept, cand)
		if strings.HasSuffix(cand, "/") {
			d++
		} else {
			u++
		}
	}
	return kept, u, d, true
}

type opKind int

const (
	opFill opKind = iota
	opFresh
	opPurge
	opPrewarm
	opTick
)

func kindName(k opKind) string {
	return [...]string{"Fill", "Fresh", "Purge", "Prewarm", "Tick"}[k]
}

func statusName(s edge.FreshStatus) string {
	return [...]string{"missing", "stale", "fresh"}[s]
}

func eqSlice(a, b []string) bool {
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

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	failLogs := []string{}
	executed := 0
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		reg := quota.NewRegistry()
		rules := purge.NewStore()
		lmax := 1 + rng.Intn(12)
		cp := edge.New(reg, rules, lmax)
		m := newNaive(lmax)

		tenants := []string{"a", "b"}
		for _, name := range tenants {
			l := quota.Limits{Qu: rng.Intn(6), Qd: rng.Intn(4), Qw: rng.Intn(8)}
			if err := reg.Register(name, l); err != nil {
				t.Fatal(err)
			}
			m.register(name, l)
		}

		segPool := []string{"img", "imgs", "css", "js", "v", "a", "b"}
		randURL := func() string {
			n := 1 + rng.Intn(3)
			parts := make([]string, n)
			for i := range parts {
				parts[i] = segPool[rng.Intn(len(segPool))] + itoa(rng.Intn(4))
			}
			return "/" + strings.Join(parts, "/")
		}
		randDir := func() string {
			url := randURL()
			return url[:strings.LastIndex(url, "/")+1]
		}

		var logb strings.Builder
		fmt.Fprintf(&logb, "seed=%d lmax=%d limits=a:%v b:%v\n",
			seed, lmax, m.limits["a"], m.limits["b"])
		now := int64(rng.Intn(3) * 86400)
		mismatch := false

		for step := 0; step < 60; step++ {
			now += int64(rng.Intn(20000)) // now 单调不减，可跨日
			tenant := tenants[rng.Intn(len(tenants))]
			var paths []string
			budget := 0
			var kind opKind
			switch rng.Intn(5) {
			case 0:
				kind = opFill
				paths = []string{randURL()}
			case 1:
				kind = opFresh
				paths = []string{randURL()}
			case 2:
				kind = opPurge
				n := 1 + rng.Intn(4)
				for i := 0; i < n; i++ {
					if rng.Intn(2) == 0 {
						paths = append(paths, randURL())
					} else {
						paths = append(paths, randDir())
					}
				}
			case 3:
				kind = opPrewarm
				n := 1 + rng.Intn(4)
				for i := 0; i < n; i++ {
					paths = append(paths, randURL())
				}
			case 4:
				kind = opTick
				budget = 1 + rng.Intn(5)
			}
			fmt.Fprintf(&logb, "step %d now=%d %s tenant=%s paths=%v budget=%d\n",
				step, now, kindName(kind), tenant, paths, budget)

			switch kind {
			case opFill:
				url := paths[0]
				err := cp.Fill(now, tenant, url)
				if err != nil {
					fmt.Fprintf(&logb, "  Fill rejected: %v\n", err)
					break
				}
				if m.cache[tenant] == nil {
					m.cache[tenant] = map[string]int64{}
				}
				m.cache[tenant][url] = m.epoch
				if now > m.maxNow {
					m.maxNow = now
				}
				fmt.Fprintf(&logb, "  Fill ok filled=%d\n", m.epoch)
			case opFresh:
				url := paths[0]
				got, err := cp.Fresh(now, tenant, url)
				want := m.fresh(tenant, url)
				fmt.Fprintf(&logb, "  Fresh got=%s(err=%v) want=%s basis=filled/fill? maxEpoch=%d\n",
					statusName(got), err, statusName(want), m.maxEpoch(tenant, url))
				if got != want {
					mismatch = true
				}
			case opPurge:
				u, d, err := cp.Purge(now, tenant, paths)
				kept, mu, md, parseOK := m.normalize(paths)
				if !parseOK {
					t.Fatalf("seed %d: model parse failed for %v", seed, paths)
				}
				m.roll(tenant, now)
				ud := m.used[tenant]
				lim := m.limits[tenant]
				var wantErr error
				switch {
				case now < m.maxNow:
					wantErr = edge.ErrClockBack
				case ud[0]+mu > lim.Qu:
					wantErr = edge.ErrURLQuota
				case ud[1]+md > lim.Qd:
					wantErr = edge.ErrDirQuota
				}
				fmt.Fprintf(&logb, "  Purge kept=%v mu=%d md=%d used=%v quota=%v got=(u=%d,d=%d,err=%v) wantErr=%v\n",
					kept, mu, md, ud, lim, u, d, err, wantErr)
				if (err == nil) != (wantErr == nil) ||
					(wantErr != nil && err != wantErr) ||
					(err == nil && (u != mu || d != md)) {
					mismatch = true
				}
				if wantErr == nil && err == nil {
					m.epoch++
					if m.rules[tenant] == nil {
						m.rules[tenant] = map[string]int64{}
					}
					for _, raw := range kept {
						if m.epoch > m.rules[tenant][raw] {
							m.rules[tenant][raw] = m.epoch
						}
					}
					m.used[tenant] = [3]int{ud[0] + mu, ud[1] + md, ud[2]}
					if now > m.maxNow {
						m.maxNow = now
					}
				}
			case opPrewarm:
				w, err := cp.Prewarm(now, tenant, paths)
				m.roll(tenant, now)
				ud := m.used[tenant]
				lim := m.limits[tenant]
				uniq := map[string]bool{}
				ordered := []string{}
				for _, u := range paths {
					if !uniq[u] {
						uniq[u] = true
						ordered = append(ordered, u)
					}
				}
				newOnes := []string{}
				for _, u := range ordered {
					if _, queued := m.inQ[tenant][u]; !queued {
						newOnes = append(newOnes, u)
					}
				}
				var wantErr error
				switch {
				case now < m.maxNow:
					wantErr = edge.ErrClockBack
				case ud[2]+len(newOnes) > lim.Qw:
					wantErr = edge.ErrWarmQuota
				case len(m.queue)+len(newOnes) > m.lmax:
					wantErr = edge.ErrQueueFull
				}
				fmt.Fprintf(&logb, "  Prewarm new=%d (from %v) used=%v qlen=%d got=(w=%d,err=%v) wantErr=%v\n",
					len(newOnes), paths, ud, len(m.queue), w, err, wantErr)
				if (err == nil) != (wantErr == nil) ||
					(wantErr != nil && err != wantErr) ||
					(err == nil && w != len(newOnes)) {
					mismatch = true
				}
				if wantErr == nil && err == nil {
					if m.inQ[tenant] == nil {
						m.inQ[tenant] = map[string]struct{}{}
					}
					for _, u := range newOnes {
						m.queue = append(m.queue, qpair{tenant, u})
						m.inQ[tenant][u] = struct{}{}
					}
					m.used[tenant] = [3]int{ud[0], ud[1], ud[2] + len(newOnes)}
					if now > m.maxNow {
						m.maxNow = now
					}
				}
			case opTick:
				lists, err := cp.Tick(now, budget)
				if err != nil {
					fmt.Fprintf(&logb, "  Tick rejected: %v\n", err)
					break
				}
				n := budget
				if n > len(m.queue) {
					n = len(m.queue)
				}
				var wantF, wantS []string
				popped := m.queue[:n]
				m.queue = m.queue[n:]
				for _, item := range popped {
					delete(m.inQ[item.tenant], item.url)
					if m.fresh(item.tenant, item.url) == edge.StatusFresh {
						wantS = append(wantS, item.url)
					} else {
						if m.cache[item.tenant] == nil {
							m.cache[item.tenant] = map[string]int64{}
						}
						m.cache[item.tenant][item.url] = m.epoch
						wantF = append(wantF, item.url)
					}
				}
				if now > m.maxNow {
					m.maxNow = now
				}
				fmt.Fprintf(&logb, "  Tick got F=%v S=%v want F=%v S=%v\n",
					lists.Filled, lists.Skipped, wantF, wantS)
				if !eqSlice(lists.Filled, wantF) || !eqSlice(lists.Skipped, wantS) {
					mismatch = true
				}
			}
			executed++
			if mismatch {
				failLogs = append(failLogs, logb.String())
				break
			}
		}
		if len(failLogs) > 3 {
			break
		}
	}
	t.Logf("differential executed %d ops across %d sequences", executed, sequences)
	if len(failLogs) > 0 {
		for _, l := range failLogs {
			t.Log(l)
		}
		t.Fatalf("%d sequences mismatched naive model", len(failLogs))
	}
}
