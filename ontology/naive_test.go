package ontology

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"
)

// naiveModel 是题面规则的朴素重写：
// 先按深度优先次序枚举全部结构路径，再对每条路径逐条检验，取首条通过者。
type naiveModel struct {
	L       int
	certs   map[string]Cert
	trusted map[string]bool
	revoked map[string]int64
}

type naiveResult struct {
	noPath     bool
	path       []string
	failReason FailReason
	failIndex  int
	failPath   []string
	considered int
}

func newNaive(L int) *naiveModel {
	return &naiveModel{L: L, certs: map[string]Cert{}, trusted: map[string]bool{}, revoked: map[string]int64{}}
}

func (m *naiveModel) candidates(c Cert) []Cert {
	var out []Cert
	for _, x := range m.certs {
		if string(x.Subject) == string(c.Issuer) &&
			string(x.Key) == string(c.AuthKey) &&
			string(x.ID) != string(c.ID) {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NotAfter != out[j].NotAfter {
			return out[i].NotAfter > out[j].NotAfter
		}
		return string(out[i].ID) < string(out[j].ID)
	})
	return out
}

// enumerate 收集全部结构路径（深度优先发现次序），同时统计取出次数。
func (m *naiveModel) enumerate(cur Cert, onPath map[string]bool, acc []Cert, paths *[][]Cert, considered *int) {
	acc = append(acc, cur)
	if m.trusted[string(cur.ID)] {
		p := make([]Cert, len(acc))
		copy(p, acc)
		*paths = append(*paths, p)
		return
	}
	for _, cand := range m.candidates(cur) {
		*considered++
		if onPath[string(cand.ID)] {
			continue
		}
		if len(acc) >= m.L {
			continue
		}
		onPath[string(cand.ID)] = true
		m.enumerate(cand, onPath, acc, paths, considered)
		delete(onPath, string(cand.ID))
	}
}

func (m *naiveModel) verify(leaf, name string, now int64) naiveResult {
	c, ok := m.certs[leaf]
	if !ok {
		return naiveResult{}
	}
	var paths [][]Cert
	considered := 0
	onPath := map[string]bool{leaf: true}
	m.enumerate(c, onPath, nil, &paths, &considered)
	if len(paths) == 0 {
		return naiveResult{noPath: true, considered: considered}
	}
	// 逐条检验（结构路径已按发现次序排列）；首条通过路径决定惰性截断点。
	firstPass := -1
	for i, p := range paths {
		if checkPath(naiveToInternal(p, m), name, now) == nil {
			firstPass = i
			break
		}
	}
	// 用第二次枚举复现惰性计数：走到首条通过路径即停。
	cnt := newNaiveCounter(m, name, now, firstPass)
	lazyConsidered := cnt.run(c, onPath)
	f := checkPath(naiveToInternal(paths[0], m), name, now)
	ids := pathIDStrs(paths[0])
	if firstPass == 0 {
		return naiveResult{path: ids, considered: lazyConsidered}
	}
	if firstPass < 0 {
		return naiveResult{failReason: f.Reason, failIndex: f.Index, failPath: ids, considered: lazyConsidered}
	}
	return naiveResult{path: pathIDStrs(paths[firstPass]), considered: lazyConsidered}
}

func pathIDStrs(p []Cert) []string {
	ids := make([]string, len(p))
	for i, x := range p {
		ids[i] = string(x.ID)
	}
	return ids
}

// naiveCounter 用朴素方式重放 DFS，但在发现第 target 条（0 基）通过结构路径时停止计数；
// target<0 表示枚举全部。
type naiveCounter struct {
	m       *naiveModel
	name    string
	now     int64
	target  int
	found   int
	stopped bool
	count   int
}

func newNaiveCounter(m *naiveModel, name string, now int64, target int) *naiveCounter {
	return &naiveCounter{m: m, name: name, now: now, target: target}
}

func (cnt *naiveCounter) run(cur Cert, onPath map[string]bool) int {
	cnt.dfs(cur, onPath, nil)
	return cnt.count
}

func (cnt *naiveCounter) dfs(cur Cert, onPath map[string]bool, acc []Cert) {
	if cnt.stopped {
		return
	}
	acc = append(acc, cur)
	if cnt.m.trusted[string(cur.ID)] {
		if checkPath(naiveToInternal(acc, cnt.m), cnt.name, cnt.now) == nil {
			cnt.found++
			if cnt.found-1 == cnt.target {
				cnt.stopped = true
			}
		}
		return
	}
	for _, cand := range cnt.m.candidates(cur) {
		cnt.count++
		if onPath[string(cand.ID)] {
			continue
		}
		if len(acc) >= cnt.m.L {
			continue
		}
		onPath[string(cand.ID)] = true
		cnt.dfs(cand, onPath, acc)
		delete(onPath, string(cand.ID))
		if cnt.stopped {
			return
		}
	}
}

func naiveToInternal(path []Cert, m *naiveModel) []*cert {
	out := make([]*cert, len(path))
	for i, c := range path {
		ra := int64(-1)
		if v, ok := m.revoked[string(c.ID)]; ok {
			ra = v
		}
		out[i] = &cert{data: c, revokedAt: ra}
	}
	return out
}

var diffLog = os.Getenv("DIFF_LOG") != ""

// TestNaiveDifferential 对 2000 组随机登记/信任/吊销/验证序列，
// 对照惰性实现与朴素全枚举实现的路径、失败原因与考察数。
func TestNaiveDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	names := []string{"a.example.com", "example.com", "b.example.com", "x.other.net", "a.b.example.com", "leaf.io"}
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		L := 1 + rng.Intn(6)
		v := mustNew(t, L, 1000)
		m := newNaive(L)

		// 先随机造一组 Subject/Key 有限的 CA 与终端，制造交叉签名与环。
		subjects := []string{"s0", "s1", "s2", "s3", "s4"}
		keys := []string{"k0", "k1", "k2", "k3", "k4"}
		var log []string
		nCerts := 2 + rng.Intn(14)
		for i := 0; i < nCerts; i++ {
			id := fmt.Sprintf("c%02d", i)
			subj := subjects[rng.Intn(len(subjects))]
			iss := subjects[rng.Intn(len(subjects))]
			key := keys[rng.Intn(len(keys))]
			auth := keys[rng.Intn(len(keys))]
			isCA := rng.Intn(3) != 0
			pl := int64(-1)
			if isCA && rng.Intn(2) == 0 {
				pl = int64(rng.Intn(3))
			}
			c := mkCert(id, subj, iss, key, auth, 0, 1+int64(rng.Intn(1200)), isCA, pl)
			if rng.Intn(3) == 0 {
				c.Permitted = []string{[]string{"example.com", ".example.com", "other.net"}[rng.Intn(3)]}
			}
			if rng.Intn(6) == 0 {
				c.Excluded = []string{[]string{"b.example.com", ".other.net"}[rng.Intn(2)]}
			}
			if !isCA || rng.Intn(2) == 0 {
				c.SAN = []string{[]string{names[rng.Intn(len(names))], "*.example.com"}[rng.Intn(2)]}
			}
			if validateCert(c) != nil {
				continue
			}
			err1 := v.Add(c)
			if err1 == nil {
				m.certs[id] = c
				log = append(log, fmt.Sprintf("ADD %s subj=%s iss=%s key=%s auth=%s ca=%v pl=%d na=%d san=%v perm=%v excl=%v",
					id, subj, iss, key, auth, isCA, pl, c.NotAfter, c.SAN, c.Permitted, c.Excluded))
			}
		}
		var liveIDs []string
		for id := range m.certs {
			liveIDs = append(liveIDs, id)
		}
		sort.Strings(liveIDs)
		if len(liveIDs) == 0 {
			continue
		}
		// 随机信任。
		for i := 0; i < 1+rng.Intn(3); i++ {
			id := liveIDs[rng.Intn(len(liveIDs))]
			if err := v.Trust([]byte(id)); err == nil {
				m.trusted[id] = true
				log = append(log, "TRUST "+id)
			}
		}
		// 随机吊销（含改早）。
		for i := 0; i < rng.Intn(4); i++ {
			id := liveIDs[rng.Intn(len(liveIDs))]
			at := int64(rng.Intn(1200))
			if err := v.Revoke([]byte(id), at); err == nil {
				if old, ok := m.revoked[id]; !ok || at < old {
					m.revoked[id] = at
				}
				log = append(log, fmt.Sprintf("REVOKE %s at=%d", id, at))
			}
		}

		leafID := liveIDs[rng.Intn(len(liveIDs))]
		name := names[rng.Intn(len(names))]
		now := int64(rng.Intn(1200))
		got, err := v.Verify([]byte(leafID), name, now)
		if err != nil {
			t.Fatalf("seed=%d verify err: %v", seed, err)
		}
		want := m.verify(leafID, name, now)

		reason := func(r *VerifyResult) string {
			if r.NoPath {
				return "NOPATH"
			}
			if r.Failure != nil {
				return fmt.Sprintf("FAIL reason=%d idx=%d path=%v", r.Failure.Reason, r.Failure.Index, byteToStr(r.Failure.Path))
			}
			return fmt.Sprintf("OK path=%v", idStrs(r))
		}
		wantStr := "NOPATH"
		if want.path != nil {
			wantStr = fmt.Sprintf("OK path=%v", want.path)
		} else if want.failPath != nil {
			wantStr = fmt.Sprintf("FAIL reason=%d idx=%d path=%v", want.failReason, want.failIndex, want.failPath)
		}
		gotStr := reason(got)
		log = append(log, fmt.Sprintf("VERIFY leaf=%s name=%s now=%d", leafID, name, now))
		log = append(log, "  got:  "+gotStr+fmt.Sprintf(" considered=%d", got.Considered))
		log = append(log, "  want: "+wantStr+fmt.Sprintf(" considered=%d", want.considered))

		if diffLog {
			for _, l := range log {
				t.Log(l)
			}
		}
		if gotStr != wantStr {
			t.Fatalf("seed=%d mismatch:\n%s\n%s", seed, gotStr, wantStr)
		}
		if got.Considered != want.considered {
			t.Fatalf("seed=%d considered mismatch got=%d want=%d", seed, got.Considered, want.considered)
		}
	}
}
