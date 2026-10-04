package inject

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/scope"
	"ontology/secret"
)

// Independent step-by-step re-implementation of the specification.
// It reuses only validation helpers (ValidName, pattern matching), never the
// production resolution/snapshot/masking logic.

type mSecret struct {
	value         string
	version       int
	protectedOnly bool
}

type mOrgSecret struct {
	mSecret
	vis   secret.Visibility
	repos []string
}

type mEnv struct {
	branches []string
	secrets  map[string]*mSecret
}

type mRepo struct {
	private bool
	pats    []string
	envs    map[string]*mEnv
	repoSec map[string]*mSecret
}

type mJob struct{ values []string }

type model struct {
	org   map[string]*mOrgSecret
	repos map[string]*mRepo
	jobs  map[int]*mJob
	next  int
}

func newModel() *model {
	return &model{org: map[string]*mOrgSecret{}, repos: map[string]*mRepo{}, jobs: map[int]*mJob{}}
}

type outcome struct {
	errClass string
	id       int
	results  []Result
	masked   string
}

func errClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, secret.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrEnvNotAllowed):
		return "env_not_allowed"
	case errors.Is(err, ErrAlreadyExists):
		return "exists"
	case errors.Is(err, secret.ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrFork):
		return "req_fork"
	case errors.Is(err, ErrProtected):
		return "req_protected"
	case errors.Is(err, ErrMissingSecret):
		return "req_missing"
	default:
		return "other:" + err.Error()
	}
}

func sameResults(a, b []Result) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Name != y.Name || x.Status != y.Status || x.Reason != y.Reason ||
			x.Level != y.Level || x.Version != y.Version {
			return false
		}
		if x.Status == StatusFound && x.Value != y.Value {
			return false
		}
	}
	return true
}

func validPatternsModel(pats []string) bool {
	for _, p := range pats {
		if !scope.ValidPattern(p) {
			return false
		}
	}
	return true
}

func (m *model) addRepo(name string, private bool, pats []string) string {
	if name == "" || !validPatternsModel(pats) {
		return "invalid"
	}
	if _, ok := m.repos[name]; ok {
		return "exists"
	}
	m.repos[name] = &mRepo{private: private, pats: append([]string(nil), pats...),
		envs: map[string]*mEnv{}, repoSec: map[string]*mSecret{}}
	return ""
}

func (m *model) addEnv(repoName, envName string, branches []string) string {
	if repoName == "" || envName == "" || !validPatternsModel(branches) {
		return "invalid"
	}
	r, ok := m.repos[repoName]
	if !ok {
		return "not_found"
	}
	if _, ok := r.envs[envName]; ok {
		return "exists"
	}
	r.envs[envName] = &mEnv{branches: append([]string(nil), branches...), secrets: map[string]*mSecret{}}
	return ""
}

func validValueModel(v string) bool { return len(v) >= 1 && len(v) <= 4096 }

func (m *model) setOrg(name, value string, vis secret.Visibility, repos []string, prot bool) string {
	if !secret.ValidName(name) || !validValueModel(value) ||
		vis < secret.All || vis > secret.Selected ||
		(vis == secret.Selected && len(repos) == 0) {
		return "invalid"
	}
	sec := m.org[name]
	if sec == nil {
		sec = &mOrgSecret{}
		m.org[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = prot
	sec.vis = vis
	sec.repos = append([]string(nil), repos...)
	return ""
}

func (m *model) setRepo(repoName, name, value string, prot bool) string {
	if !secret.ValidName(name) || !validValueModel(value) {
		return "invalid"
	}
	r, ok := m.repos[repoName]
	if !ok {
		return "not_found"
	}
	sec := r.repoSec[name]
	if sec == nil {
		sec = &mSecret{}
		r.repoSec[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = prot
	return ""
}

func (m *model) setEnv(repoName, envName, name, value string, prot bool) string {
	if !secret.ValidName(name) || !validValueModel(value) {
		return "invalid"
	}
	r, ok := m.repos[repoName]
	if !ok {
		return "not_found"
	}
	e, ok := r.envs[envName]
	if !ok {
		return "not_found"
	}
	sec := e.secrets[name]
	if sec == nil {
		sec = &mSecret{}
		e.secrets[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = prot
	return ""
}

func (m *model) delOrg(name string) string {
	if !secret.ValidName(name) {
		return "invalid"
	}
	if _, ok := m.org[name]; !ok {
		return "not_found"
	}
	delete(m.org, name)
	return ""
}

func (m *model) delRepo(repoName, name string) string {
	if !secret.ValidName(name) {
		return "invalid"
	}
	r, ok := m.repos[repoName]
	if !ok {
		return "not_found"
	}
	if _, ok := r.repoSec[name]; !ok {
		return "not_found"
	}
	delete(r.repoSec, name)
	return ""
}

func (m *model) delEnv(repoName, envName, name string) string {
	if !secret.ValidName(name) {
		return "invalid"
	}
	r, ok := m.repos[repoName]
	if !ok {
		return "not_found"
	}
	e, ok := r.envs[envName]
	if !ok {
		return "not_found"
	}
	if _, ok := e.secrets[name]; !ok {
		return "not_found"
	}
	delete(e.secrets, name)
	return ""
}

func (m *model) orgVisible(sec *mOrgSecret, repoName string, private bool) bool {
	switch sec.vis {
	case secret.All:
		return true
	case secret.Private:
		return private
	case secret.Selected:
		for _, r := range sec.repos {
			if r == repoName {
				return true
			}
		}
	}
	return false
}

func (m *model) inject(job Job) outcome {
	if job.Repo == "" || job.Ref == "" ||
		!(job.Event == Push || job.Event == PRInternal || job.Event == PRFork) ||
		len(job.Wants) < 1 || len(job.Wants) > 64 {
		return outcome{errClass: "invalid"}
	}
	seen := map[string]bool{}
	for _, w := range job.Wants {
		if !secret.ValidName(w.Name) || seen[w.Name] {
			return outcome{errClass: "invalid"}
		}
		seen[w.Name] = true
	}
	r, ok := m.repos[job.Repo]
	if !ok {
		return outcome{errClass: "not_found"}
	}
	var e *mEnv
	if job.Env != "" {
		e, ok = r.envs[job.Env]
		if !ok {
			return outcome{errClass: "not_found"}
		}
	}
	if job.Env != "" && len(e.branches) > 0 && !scope.MatchAny(e.branches, job.Ref) {
		return outcome{errClass: "env_not_allowed"}
	}

	protected := job.Event == Push && scope.MatchAny(r.pats, job.Ref)
	results := make([]Result, len(job.Wants))
	var snap []string

	for i, w := range job.Wants {
		if job.Event == PRFork {
			results[i] = Result{Name: w.Name, Status: StatusWithheld, Reason: ReasonFork}
			continue
		}
		var found *mSecret
		level := secret.Level(-1)
		if e != nil {
			if sec := e.secrets[w.Name]; sec != nil {
				found = sec
				level = secret.EnvLevel
			}
		}
		if found == nil {
			if sec := r.repoSec[w.Name]; sec != nil {
				found = sec
				level = secret.RepoLevel
			}
		}
		if found == nil {
			if sec := m.org[w.Name]; sec != nil && m.orgVisible(sec, job.Repo, r.private) {
				found = &sec.mSecret
				level = secret.OrgLevel
			}
		}
		if found == nil {
			results[i] = Result{Name: w.Name, Status: StatusNotFound}
			continue
		}
		if found.protectedOnly && !protected {
			results[i] = Result{Name: w.Name, Status: StatusWithheld, Reason: ReasonProtected}
			continue
		}
		results[i] = Result{Name: w.Name, Status: StatusFound, Value: found.value,
			Level: level, Version: found.version}
		snap = append(snap, found.value)
	}

	for i, w := range job.Wants {
		if w.Required && results[i].Status != StatusFound {
			res := results[i]
			cls := "req_missing"
			if res.Status == StatusWithheld {
				if res.Reason == ReasonFork {
					cls = "req_fork"
				} else {
					cls = "req_protected"
				}
			}
			return outcome{errClass: cls, results: results}
		}
	}

	m.next++
	m.jobs[m.next] = &mJob{values: snap}
	return outcome{id: m.next, results: results}
}

func naiveMask(values []string, line string) string {
	type sp struct{ s, e int }
	var spans []sp
	for _, v := range values {
		if len(v) < 4 {
			continue
		}
		for i := 0; i+len(v) <= len(line); i++ {
			if line[i:i+len(v)] == v {
				spans = append(spans, sp{i, i + len(v)})
			}
		}
	}
	if len(spans) == 0 {
		return line
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].s != spans[j].s {
			return spans[i].s < spans[j].s
		}
		return spans[i].e < spans[j].e
	})
	merged := []sp{spans[0]}
	for _, cur := range spans[1:] {
		last := &merged[len(merged)-1]
		if cur.s <= last.e {
			if cur.e > last.e {
				last.e = cur.e
			}
		} else {
			merged = append(merged, cur)
		}
	}
	var b strings.Builder
	prev := 0
	for _, x := range merged {
		b.WriteString(line[prev:x.s])
		b.WriteString("***")
		prev = x.e
	}
	b.WriteString(line[prev:])
	return b.String()
}

func (m *model) mask(id int, line string) outcome {
	j, ok := m.jobs[id]
	if !ok {
		return outcome{errClass: "not_found"}
	}
	return outcome{masked: naiveMask(j.values, line)}
}

var (
	fuzzNames    = []string{"TOKEN", "KEY", "NPM", "DB", "A1", "_X", "SECRET2", "ZZ"}
	fuzzBranches = []string{"main", "feat", "release-1", "release-2", "dev"}
	fuzzValues   = []string{"abcd", "cdef", "abab", "abc", "o1", "oldsecret9", "newsecret9", "v4"}
	fuzzEnvs     = []string{"prod", "staging"}
	fuzzLogLines = []string{"abcdef", "xababab", "abcdabcd", "abcd-abcd", "abc", "token abcd end cdef"}
)

func pickPatterns(rng *rand.Rand) []string {
	n := rng.Intn(3)
	var out []string
	base := []string{"main", "release*", "*", "feat"}
	for i := 0; i < n; i++ {
		out = append(out, base[rng.Intn(len(base))])
	}
	return out
}

func pickRepos(rng *rand.Rand) []string {
	n := 1 + rng.Intn(2)
	perm := rng.Perm(2)
	out := make([]string, n)
	for i := range out {
		out[i] = "r" + string(rune('1'+perm[i]))
	}
	return out
}

func randomJob(rng *rand.Rand) Job {
	wc := 1 + rng.Intn(4)
	perm := rng.Perm(len(fuzzNames))
	wants := make([]Want, wc)
	for i := range wants {
		wants[i] = Want{Name: fuzzNames[perm[i]], Required: rng.Intn(2) == 0}
	}
	env := ""
	if rng.Intn(2) == 0 {
		env = fuzzEnvs[rng.Intn(len(fuzzEnvs))]
	}
	events := []Event{Push, PRInternal, PRFork}
	return Job{
		Repo:  "r" + string(rune('1'+rng.Intn(2))),
		Ref:   fuzzBranches[rng.Intn(len(fuzzBranches))],
		Event: events[rng.Intn(3)],
		Env:   env,
		Wants: wants,
	}
}

func describeResults(rs []Result) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		switch r.Status {
		case StatusFound:
			parts[i] = fmt.Sprintf("%s=found(%s,v%d,L%d)", r.Name, r.Value, r.Version, r.Level)
		case StatusNotFound:
			parts[i] = r.Name + "=NotFound"
		default:
			parts[i] = fmt.Sprintf("%s=Withheld(%s)", r.Name, r.Reason)
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			store := secret.NewStore()
			svc := NewService(store)
			md := newModel()

			for i, rn := range []string{"r1", "r2"} {
				pats := pickPatterns(rng)
				errS := store.AddRepo(rn, i == 0, pats)
				clsM := md.addRepo(rn, i == 0, pats)
				t.Logf("seed=%d AddRepo(%s private=%v pats=%v) -> real=%s model=%s",
					seed, rn, i == 0, pats, errClass(errS), clsM)
				if errClass(errS) != clsM {
					t.Fatalf("AddRepo mismatch real=%v model=%s", errS, clsM)
				}
			}
			for _, rn := range []string{"r1", "r2"} {
				for _, en := range fuzzEnvs {
					branches := pickPatterns(rng)
					errS := store.AddEnv(rn, en, branches)
					clsM := md.addEnv(rn, en, branches)
					t.Logf("seed=%d AddEnv(%s,%s,%v) -> real=%s model=%s",
						seed, rn, en, branches, errClass(errS), clsM)
					if errClass(errS) != clsM {
						t.Fatalf("AddEnv mismatch real=%v model=%s", errS, clsM)
					}
				}
			}

			knownJobs := []int{}
			for op := 0; op < 40; op++ {
				switch rng.Intn(8) {
				case 0:
					name, value, prot := fuzzNames[rng.Intn(len(fuzzNames))],
						fuzzValues[rng.Intn(len(fuzzValues))], rng.Intn(2) == 0
					vis := []secret.Visibility{secret.All, secret.Private, secret.Selected}[rng.Intn(3)]
					repos := pickRepos(rng)
					errS := store.SetOrg(name, value, vis, repos, prot)
					clsM := md.setOrg(name, value, vis, repos, prot)
					t.Logf("seed=%d op=%d SetOrg(%s,%s,vis=%d,repos=%v,prot=%v) -> real=%s model=%s",
						seed, op, name, value, vis, repos, prot, errClass(errS), clsM)
					if errClass(errS) != clsM {
						t.Fatalf("SetOrg mismatch real=%v model=%s", errS, clsM)
					}
				case 1, 2:
					repoName, name := "r"+string(rune('1'+rng.Intn(2))), fuzzNames[rng.Intn(len(fuzzNames))]
					value, prot := fuzzValues[rng.Intn(len(fuzzValues))], rng.Intn(2) == 0
					var errS error
					var clsM string
					if op%2 == 0 {
						errS = store.SetRepo(repoName, name, value, prot)
						clsM = md.setRepo(repoName, name, value, prot)
					} else {
						en := fuzzEnvs[rng.Intn(len(fuzzEnvs))]
						errS = store.SetEnv(repoName, en, name, value, prot)
						clsM = md.setEnv(repoName, en, name, value, prot)
					}
					t.Logf("seed=%d op=%d SetLevel repo=%s name=%s value=%s prot=%v -> real=%s model=%s",
						seed, op, repoName, name, value, prot, errClass(errS), clsM)
					if errClass(errS) != clsM {
						t.Fatalf("set mismatch real=%v model=%s", errS, clsM)
					}
				case 3:
					name := fuzzNames[rng.Intn(len(fuzzNames))]
					repoName := "r" + string(rune('1'+rng.Intn(2)))
					var errS error
					var clsM string
					switch rng.Intn(3) {
					case 0:
						errS = store.DeleteOrg(name)
						clsM = md.delOrg(name)
					case 1:
						errS = store.DeleteRepoSecret(repoName, name)
						clsM = md.delRepo(repoName, name)
					default:
						en := fuzzEnvs[rng.Intn(len(fuzzEnvs))]
						errS = store.DeleteEnvSecret(repoName, en, name)
						clsM = md.delEnv(repoName, en, name)
					}
					t.Logf("seed=%d op=%d Delete(%s,%s) -> real=%s model=%s",
						seed, op, repoName, name, errClass(errS), clsM)
					if errClass(errS) != clsM {
						t.Fatalf("delete mismatch real=%v model=%s", errS, clsM)
					}
				default:
					if rng.Intn(4) == 0 && len(knownJobs) > 0 {
						id := knownJobs[rng.Intn(len(knownJobs))]
						line := fuzzLogLines[rng.Intn(len(fuzzLogLines))]
						got, errS := svc.Mask(id, line)
						mo := md.mask(id, line)
						t.Logf("seed=%d op=%d Mask(job=%d,%q) -> real=%q(%s) model=%q(%s)",
							seed, op, id, line, got, errClass(errS), mo.masked, mo.errClass)
						if errClass(errS) != mo.errClass || got != mo.masked {
							t.Fatalf("mask mismatch real=%q(%v) model=%q(%s)", got, errS, mo.masked, mo.errClass)
						}
					} else {
						job := randomJob(rng)
						id, rs, errS := svc.Inject(job)
						mo := md.inject(job)
						t.Logf("seed=%d op=%d Inject(%s,%s,%s,env=%s,wants=%d) -> real id=%d %s (%s); model id=%d %s (%s)",
							seed, op, job.Repo, job.Ref, job.Event, job.Env, len(job.Wants),
							id, describeResults(rs), errClass(errS),
							mo.id, describeResults(mo.results), mo.errClass)
						if errClass(errS) != mo.errClass || id != mo.id ||
							(errS == nil && !sameResults(rs, mo.results)) {
							t.Fatalf("inject mismatch:\nreal id=%d %s err=%s\nmodel id=%d %s err=%s",
								id, describeResults(rs), errClass(errS),
								mo.id, describeResults(mo.results), mo.errClass)
						}
						if errS == nil {
							knownJobs = append(knownJobs, id)
						}
					}
				}
			}
		})
	}
}

func TestConcurrentSmoke(t *testing.T) {
	store := secret.NewStore()
	if err := store.AddRepo("r1", true, []string{"main"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEnv("r1", "prod", []string{"main"}); err != nil {
		t.Fatal(err)
	}
	_ = store.SetOrg("TOKEN", "abcd", secret.All, nil, false)
	svc := NewService(store)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				_ = store.SetOrg("TOKEN", "abcd", secret.All, nil, i%2 == 0)
				id, _, err := svc.Inject(Job{Repo: "r1", Ref: "main", Event: Push,
					Wants: []Want{{Name: "TOKEN"}}})
				if err == nil {
					_, _ = svc.Mask(id, "x=abcd")
				}
			}
		}(i)
	}
	wg.Wait()
}
