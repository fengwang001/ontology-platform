package inject

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/scope"
	"ontology/secret"
)

// 本测试用独立编写的“朴素逐步模型”重放同一随机操作序列，逐步对照真实实现的
// 输入、输出与判定依据；同时用同一序列重放两次，验证结果确定（可复现）。

type nDef struct {
	value string
	ver   int
	prot  bool
}

type nOrg struct {
	nDef
	vis scope.Visibility
	sel []string
}

type nRepo struct {
	private   bool
	protected []string
	envs      map[string][]string
	secrets   map[string]nDef
	envSec    map[string]map[string]nDef
}

type nJob struct {
	values []string
}

type naive struct {
	repos map[string]*nRepo
	org   map[string]nOrg
	jobs  map[int]nJob
	next  int
}

func newNaive() *naive {
	return &naive{
		repos: map[string]*nRepo{},
		org:   map[string]nOrg{},
		jobs:  map[int]nJob{},
	}
}

func nMatch(p, ref string) bool {
	if p == "" {
		return false
	}
	if p[len(p)-1] != '*' {
		return p == ref
	}
	pre := p[:len(p)-1]
	return strings.HasPrefix(ref, pre)
}

func nValidPattern(p string) bool {
	if p == "" {
		return false
	}
	return strings.Count(p, "*") == 0 || (strings.HasSuffix(p, "*") && strings.Count(p, "*") == 1)
}

func nValidName(n string) bool {
	if len(n) == 0 || len(n) > 64 {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		ok := c == '_' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}

func nValidValue(v string) bool { return len(v) >= 1 && len(v) <= 4096 }

func clone(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (n *naive) addRepo(repo string, private bool, pats []string) error {
	if repo == "" {
		return secret.ErrInvalid
	}
	for _, p := range pats {
		if !nValidPattern(p) {
			return secret.ErrInvalid
		}
	}
	if _, ok := n.repos[repo]; ok {
		return secret.ErrExists
	}
	n.repos[repo] = &nRepo{
		private:   private,
		protected: clone(pats),
		envs:      map[string][]string{},
		secrets:   map[string]nDef{},
		envSec:    map[string]map[string]nDef{},
	}
	return nil
}

func (n *naive) addEnv(repo, env string, branches []string) error {
	if repo == "" || env == "" {
		return secret.ErrInvalid
	}
	for _, p := range branches {
		if !nValidPattern(p) {
			return secret.ErrInvalid
		}
	}
	r, ok := n.repos[repo]
	if !ok {
		return secret.ErrRepoNotFound
	}
	if _, ok := r.envs[env]; ok {
		return secret.ErrExists
	}
	r.envs[env] = clone(branches)
	return nil
}

func (n *naive) setOrg(name, value string, vis scope.Visibility, repos []string, prot bool) error {
	if !nValidName(name) || !nValidValue(value) || vis < scope.All || vis > scope.Selected {
		return secret.ErrInvalid
	}
	for _, r := range repos {
		if r == "" {
			return secret.ErrInvalid
		}
	}
	d := n.org[name]
	d.value, d.ver, d.prot, d.vis, d.sel = value, d.ver+1, prot, vis, clone(repos)
	n.org[name] = d
	return nil
}

func (n *naive) setRepo(repo, name, value string, prot bool) error {
	if repo == "" || !nValidName(name) || !nValidValue(value) {
		return secret.ErrInvalid
	}
	r, ok := n.repos[repo]
	if !ok {
		return secret.ErrRepoNotFound
	}
	d := r.secrets[name]
	d.value, d.ver, d.prot = value, d.ver+1, prot
	r.secrets[name] = d
	return nil
}

func (n *naive) setEnv(repo, env, name, value string, prot bool) error {
	if repo == "" || env == "" || !nValidName(name) || !nValidValue(value) {
		return secret.ErrInvalid
	}
	r, ok := n.repos[repo]
	if !ok {
		return secret.ErrRepoNotFound
	}
	if _, ok := r.envs[env]; !ok {
		return secret.ErrEnvNotFound
	}
	if r.envSec[env] == nil {
		r.envSec[env] = map[string]nDef{}
	}
	d := r.envSec[env][name]
	d.value, d.ver, d.prot = value, d.ver+1, prot
	r.envSec[env][name] = d
	return nil
}

func (n *naive) delOrg(name string) error {
	if !nValidName(name) {
		return secret.ErrInvalid
	}
	if _, ok := n.org[name]; !ok {
		return secret.ErrSecretNotFound
	}
	delete(n.org, name)
	return nil
}

func (n *naive) delRepo(repo, name string) error {
	if repo == "" || !nValidName(name) {
		return secret.ErrInvalid
	}
	r, ok := n.repos[repo]
	if !ok {
		return secret.ErrRepoNotFound
	}
	if _, ok := r.secrets[name]; !ok {
		return secret.ErrSecretNotFound
	}
	delete(r.secrets, name)
	return nil
}

func (n *naive) delEnv(repo, env, name string) error {
	if repo == "" || env == "" || !nValidName(name) {
		return secret.ErrInvalid
	}
	r, ok := n.repos[repo]
	if !ok {
		return secret.ErrRepoNotFound
	}
	if _, ok := r.envs[env]; !ok {
		return secret.ErrEnvNotFound
	}
	if _, ok := r.envSec[env][name]; !ok {
		return secret.ErrSecretNotFound
	}
	delete(r.envSec[env], name)
	return nil
}

// naiveInject 严格按题面次序逐步模拟一次作业注入。
func (n *naive) inject(job Job) (int, []string, []resultTriple, error) {
	if job.Repo == "" || job.Ref == "" {
		return 0, nil, nil, ErrInvalid
	}
	switch job.Event {
	case "push", "pr_internal", "pr_fork":
	default:
		return 0, nil, nil, ErrInvalid
	}
	if len(job.Wants) < 1 || len(job.Wants) > 64 {
		return 0, nil, nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, wnt := range job.Wants {
		if !nValidName(wnt.Name) || seen[wnt.Name] {
			return 0, nil, nil, ErrInvalid
		}
		seen[wnt.Name] = true
	}
	r, ok := n.repos[job.Repo]
	if !ok {
		return 0, nil, nil, ErrRepoNotFound
	}
	var branches []string
	if job.Env != "" {
		branches, ok = r.envs[job.Env]
		if !ok {
			return 0, nil, nil, ErrEnvNotFound
		}
		allowed := len(branches) == 0
		for _, p := range branches {
			if nMatch(p, job.Ref) {
				allowed = true
			}
		}
		if !allowed {
			return 0, nil, nil, ErrEnvDenied
		}
	}
	triples := make([]resultTriple, len(job.Wants))
	var snapshot []string
	if job.Event == "pr_fork" {
		for i := range job.Wants {
			triples[i] = resultTriple{kind: "fork"}
		}
	} else {
		protected := job.Event == "push"
		if protected {
			protected = false
			for _, p := range r.protected {
				if nMatch(p, job.Ref) {
					protected = true
				}
			}
		}
		for i, wnt := range job.Wants {
			var d nDef
			var lvl string
			found := false
			if job.Env != "" {
				if dd, ok := r.envSec[job.Env][wnt.Name]; ok {
					d, lvl, found = dd, "env", true
				}
			}
			if !found {
				if dd, ok := r.secrets[wnt.Name]; ok {
					d, lvl, found = dd, "repo", true
				}
			}
			if !found {
				if od, ok := n.org[wnt.Name]; ok {
					visible := od.vis == scope.All ||
						od.vis == scope.Private && r.private
					if od.vis == scope.Selected {
						visible = false
						for _, rr := range od.sel {
							if rr == job.Repo {
								visible = true
							}
						}
					}
					if visible {
						d, lvl, found = od.nDef, "org", true
					}
				}
			}
			switch {
			case !found:
				triples[i] = resultTriple{kind: "missing"}
			case d.prot && !protected:
				triples[i] = resultTriple{kind: "protected"}
			default:
				triples[i] = resultTriple{kind: "found", value: d.value, level: lvl, ver: d.ver}
				snapshot = append(snapshot, d.value)
			}
		}
	}
	for i, wnt := range job.Wants {
		if wnt.Required && triples[i].kind != "found" {
			var reason error
			switch triples[i].kind {
			case "fork":
				reason = ErrWithheldFork
			case "protected":
				reason = ErrWithheldProtect
			default:
				reason = ErrNotFound
			}
			return 0, nil, nil, &Failure{Name: wnt.Name, Reason: reason}
		}
	}
	n.next++
	n.jobs[n.next] = nJob{values: snapshot}
	return n.next, snapshot, triples, nil
}

// naiveMask 独立实现：收集全部出现（允许重叠），按起点排序合并相接区间。
func (n *naive) mask(id int, line string) (string, error) {
	job, ok := n.jobs[id]
	if !ok {
		return "", ErrJobNotFound
	}
	type iv struct{ s, e int }
	var ivs []iv
	dedup := map[string]bool{}
	vals := append([]string{}, job.values...)
	sort.Strings(vals)
	for _, v := range vals {
		if len(v) < 4 || dedup[v] {
			continue
		}
		dedup[v] = true
		for i := 0; i+len(v) <= len(line); i++ {
			if line[i:i+len(v)] == v {
				ivs = append(ivs, iv{i, i + len(v)})
			}
		}
	}
	if len(ivs) == 0 {
		return line, nil
	}
	sort.Slice(ivs, func(i, j int) bool {
		if ivs[i].s != ivs[j].s {
			return ivs[i].s < ivs[j].s
		}
		return ivs[i].e < ivs[j].e
	})
	merged := []iv{ivs[0]}
	for _, x := range ivs[1:] {
		last := &merged[len(merged)-1]
		if x.s <= last.e {
			if x.e > last.e {
				last.e = x.e
			}
		} else {
			merged = append(merged, x)
		}
	}
	var b strings.Builder
	cur := 0
	for _, m := range merged {
		b.WriteString(line[cur:m.s])
		b.WriteString("***")
		cur = m.e
	}
	b.WriteString(line[cur:])
	return b.String(), nil
}

type resultTriple struct {
	kind  string
	value string
	level string
	ver   int
}

type opKind int

const (
	opAddRepo opKind = iota
	opAddEnv
	opSetOrg
	opSetRepo
	opSetEnv
	opDelOrg
	opDelRepo
	opDelEnv
	opInject
	opMask
)

type op struct {
	kind                   opKind
	repo, env, name, value string
	ref, event             string
	private, prot          bool
	vis                    scope.Visibility
	repos, pats, branches  []string
	wants                  []Want
	jobRef                 int
	line                   string
}

func genOp(rng *rand.Rand) op {
	repos := []string{"r1", "r2", "r3"}
	envs := []string{"prod", "dev"}
	names := []string{"TOKEN", "DB", "NPM", "KEY", "A1", "MISSING", "SHORTX", "dup"}
	badNames := []string{"lower", "1BAD", "BAD-NAME", ""}
	refs := []string{"main", "release-1", "feat", "x", "release-2"}
	events := []string{"push", "pr_internal", "pr_fork"}
	patternPool := []string{"main", "release*", "*", "feat", "bad*d", ""}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }

	switch opKind(rng.Intn(10)) {
	case opAddRepo:
		repo := pick(repos)
		if rng.Intn(6) == 0 {
			repo = pick(badNames)
		}
		return op{kind: opAddRepo, repo: repo, private: rng.Intn(2) == 0,
			pats: pickPatterns(rng, patternPool)}
	case opAddEnv:
		return op{kind: opAddEnv, repo: pick(repos), env: pick(envs),
			branches: pickPatterns(rng, patternPool)}
	case opSetOrg:
		name := pick(names)
		if rng.Intn(6) == 0 {
			name = pick(badNames)
		}
		vis := scope.Visibility(rng.Intn(3))
		var sel []string
		if vis == scope.Selected && rng.Intn(2) == 0 {
			sel = []string{pick(repos)}
		}
		return op{kind: opSetOrg, name: name, value: pickValue(rng), vis: vis,
			repos: sel, prot: rng.Intn(3) == 0}
	case opSetRepo:
		return op{kind: opSetRepo, repo: pick(repos), name: maybeBad(rng, pick(names), badNames),
			value: pickValue(rng), prot: rng.Intn(3) == 0}
	case opSetEnv:
		return op{kind: opSetEnv, repo: pick(repos), env: pick(envs),
			name: maybeBad(rng, pick(names), badNames), value: pickValue(rng),
			prot: rng.Intn(3) == 0}
	case opDelOrg:
		return op{kind: opDelOrg, name: maybeBad(rng, pick(names), badNames)}
	case opDelRepo:
		return op{kind: opDelRepo, repo: pick(repos), name: maybeBad(rng, pick(names), badNames)}
	case opDelEnv:
		return op{kind: opDelEnv, repo: pick(repos), env: pick(envs),
			name: maybeBad(rng, pick(names), badNames)}
	case opInject:
		nw := 1 + rng.Intn(3)
		wants := make([]Want, 0, nw)
		used := map[string]bool{}
		for len(wants) < nw {
			nm := pick(names)
			if used[nm] {
				continue
			}
			used[nm] = true
			wants = append(wants, Want{Name: nm, Required: rng.Intn(2) == 0})
		}
		if rng.Intn(8) == 0 {
			wants[0].Name = pick(badNames)
		}
		env := ""
		if rng.Intn(2) == 0 {
			env = pick(envs)
		}
		return op{kind: opInject, repo: pick(repos), ref: pick(refs),
			event: pick(events), env: env, wants: wants}
	default:
		return op{kind: opMask, jobRef: rng.Intn(4), line: pickLine(rng)}
	}
}

func maybeBad(rng *rand.Rand, good string, bad []string) string {
	if rng.Intn(6) == 0 {
		return bad[rng.Intn(len(bad))]
	}
	return good
}

func pickPatterns(rng *rand.Rand, pool []string) []string {
	if rng.Intn(3) == 0 {
		return nil
	}
	k := 1 + rng.Intn(2)
	out := make([]string, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, pool[rng.Intn(len(pool))])
	}
	return out
}

func pickValue(rng *rand.Rand) string {
	switch rng.Intn(8) {
	case 0:
		return ""
	case 1:
		return "abc"
	case 2:
		return "abcd"
	case 3:
		return "abab"
	case 4:
		return "cdef"
	default:
		return "val" + fmt.Sprintf("%d", rng.Intn(40))
	}
}

func pickLine(rng *rand.Rand) string {
	parts := []string{"abcd", "cdef", "abab", "x", "-", "abcdef", "xababab", "abcd-abcd", "noop"}
	return parts[rng.Intn(len(parts))]
}

func classify(err error) string {
	for _, c := range []struct {
		e    error
		name string
	}{
		{secret.ErrInvalid, "Invalid"},
		{secret.ErrRepoNotFound, "RepoNotFound"},
		{secret.ErrEnvNotFound, "EnvNotFound"},
		{secret.ErrExists, "Exists"},
		{secret.ErrSecretNotFound, "SecretNotFound"},
		{ErrEnvDenied, "EnvDenied"},
		{ErrJobNotFound, "JobNotFound"},
		{ErrWithheldFork, "WithheldFork"},
		{ErrWithheldProtect, "WithheldProtected"},
		{ErrNotFound, "NotFound"},
	} {
		if errors.Is(err, c.e) {
			return c.name
		}
	}
	if err == nil {
		return "OK"
	}
	return "OTHER:" + err.Error()
}

// applyReal 在真实实现上执行一条操作，返回归一化结果描述。
func applyReal(store *secret.Store, svc *Service, o op, jobIDs []int) string {
	switch o.kind {
	case opAddRepo:
		return classify(store.AddRepo(o.repo, o.private, o.pats))
	case opAddEnv:
		return classify(store.AddEnv(o.repo, o.env, o.branches))
	case opSetOrg:
		return classify(store.SetOrg(o.name, o.value, o.vis, o.repos, o.prot))
	case opSetRepo:
		return classify(store.SetRepo(o.repo, o.name, o.value, o.prot))
	case opSetEnv:
		return classify(store.SetEnv(o.repo, o.env, o.name, o.value, o.prot))
	case opDelOrg:
		return classify(store.DeleteOrg(o.name))
	case opDelRepo:
		return classify(store.DeleteRepoSecret(o.repo, o.name))
	case opDelEnv:
		return classify(store.DeleteEnv(o.repo, o.env, o.name))
	case opInject:
		out, err := svc.Inject(Job{o.repo, o.ref, o.event, o.env, o.wants})
		if err != nil {
			return classify(err)
		}
		return describeOutcome(out)
	default:
		id := pickJobID(jobIDs, o.jobRef)
		got, err := svc.Mask(id, o.line)
		if err != nil {
			return classify(err)
		}
		return "Mask=" + got
	}
}

// applyNaive 在朴素模型上执行同一条操作。
func applyNaive(n *naive, o op, jobIDs []int) string {
	switch o.kind {
	case opAddRepo:
		return classify(n.addRepo(o.repo, o.private, o.pats))
	case opAddEnv:
		return classify(n.addEnv(o.repo, o.env, o.branches))
	case opSetOrg:
		return classify(n.setOrg(o.name, o.value, o.vis, o.repos, o.prot))
	case opSetRepo:
		return classify(n.setRepo(o.repo, o.name, o.value, o.prot))
	case opSetEnv:
		return classify(n.setEnv(o.repo, o.env, o.name, o.value, o.prot))
	case opDelOrg:
		return classify(n.delOrg(o.name))
	case opDelRepo:
		return classify(n.delRepo(o.repo, o.name))
	case opDelEnv:
		return classify(n.delEnv(o.repo, o.env, o.name))
	case opInject:
		id, _, triples, err := n.inject(Job{o.repo, o.ref, o.event, o.env, o.wants})
		if err != nil {
			return classify(err)
		}
		return describeTriples(id, triples)
	default:
		id := pickJobID(jobIDs, o.jobRef)
		got, err := n.mask(id, o.line)
		if err != nil {
			return classify(err)
		}
		return "Mask=" + got
	}
}

func describeOutcome(out *Outcome) string {
	s := fmt.Sprintf("Inject#%d[", out.JobID)
	for i, r := range out.Results {
		if i > 0 {
			s += ","
		}
		switch {
		case r.Found != nil:
			lvl := "env"
			switch r.Found.Level {
			case LevelRepo:
				lvl = "repo"
			case LevelOrg:
				lvl = "org"
			}
			s += fmt.Sprintf("F(%s,%s,v%d)", r.Found.Value, lvl, r.Found.Version)
		case errors.Is(r.Withheld, ErrWithheldFork):
			s += "Wfork"
		case errors.Is(r.Withheld, ErrWithheldProtect):
			s += "Wprot"
		default:
			s += "N"
		}
	}
	return s + "]"
}

func describeTriples(id int, ts []resultTriple) string {
	s := fmt.Sprintf("Inject#%d[", id)
	for i, t := range ts {
		if i > 0 {
			s += ","
		}
		switch t.kind {
		case "found":
			s += fmt.Sprintf("F(%s,%s,v%d)", t.value, t.level, t.ver)
		case "fork":
			s += "Wfork"
		case "protected":
			s += "Wprot"
		default:
			s += "N"
		}
	}
	return s + "]"
}

func pickJobID(ids []int, ref int) int {
	if len(ids) == 0 {
		return ref
	}
	return ids[ref%len(ids)]
}

func opString(o op) string {
	switch o.kind {
	case opAddRepo:
		return fmt.Sprintf("AddRepo(%q,priv=%v,pats=%q)", o.repo, o.private, o.pats)
	case opAddEnv:
		return fmt.Sprintf("AddEnv(%q,%q,br=%q)", o.repo, o.env, o.branches)
	case opSetOrg:
		return fmt.Sprintf("SetOrg(%q,%q,vis=%d,sel=%q,prot=%v)", o.name, o.value, o.vis, o.repos, o.prot)
	case opSetRepo:
		return fmt.Sprintf("SetRepo(%q,%q,%q,prot=%v)", o.repo, o.name, o.value, o.prot)
	case opSetEnv:
		return fmt.Sprintf("SetEnv(%q,%q,%q,%q,prot=%v)", o.repo, o.env, o.name, o.value, o.prot)
	case opDelOrg:
		return fmt.Sprintf("DeleteOrg(%q)", o.name)
	case opDelRepo:
		return fmt.Sprintf("DeleteRepo(%q,%q)", o.repo, o.name)
	case opDelEnv:
		return fmt.Sprintf("DeleteEnv(%q,%q,%q)", o.repo, o.env, o.name)
	case opInject:
		return fmt.Sprintf("Inject(%q,%q,%q,%q,%v)", o.repo, o.ref, o.event, o.env, o.wants)
	default:
		return fmt.Sprintf("Mask(job#%d,%q)", o.jobRef, o.line)
	}
}

// runSequence 在真实实现与朴素模型上重放同一序列，逐步对照。
func runSequence(t *testing.T, seq []op, seed int64, tag string) {
	t.Helper()
	store := secret.New()
	svc := New(store)
	n := newNaive()
	var realIDs, naiveIDs []int
	for i, o := range seq {
		real := applyReal(store, svc, o, realIDs)
		model := applyNaive(n, o, naiveIDs)
		t.Logf("[seed=%d %s] op%02d %s => real=%s | naive=%s", seed, tag, i, opString(o), real, model)
		if real != model {
			t.Fatalf("MISMATCH seed=%d op%02d %s\n real : %s\n naive: %s", seed, i, opString(o), real, model)
		}
		// 两边的作业号集合必须同步，Mask 才能引用同一批作业。
		if o.kind == opInject && len(real) > 7 && real[:7] == "Inject#" {
			var id int
			fmt.Sscanf(real, "Inject#%d", &id)
			realIDs = append(realIDs, id)
			var nid int
			fmt.Sscanf(model, "Inject#%d", &nid)
			naiveIDs = append(naiveIDs, nid)
			if id != nid {
				t.Fatalf("job id divergence real=%d naive=%d", id, nid)
			}
		}
	}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 16
	for s := 0; s < sequences; s++ {
		seed := int64(20261005 + s)
		rng := rand.New(rand.NewSource(seed))
		seq := make([]op, 0, opsPerSeq)
		// 起始保证一个合法仓库，提高后续路径覆盖率。
		seq = append(seq, op{kind: opAddRepo, repo: "r1", private: true,
			pats: []string{"main", "release*"}})
		for len(seq) < opsPerSeq {
			seq = append(seq, genOp(rng))
		}
		runSequence(t, seq, seed, "A")
	}
}

func TestReplayDeterminism(t *testing.T) {
	// 相同序列重放：第二次结果必须与第一次完全一致。
	rng := rand.New(rand.NewSource(42))
	seq := []op{{kind: opAddRepo, repo: "r1", private: true, pats: []string{"main"}}}
	for i := 0; i < 20; i++ {
		seq = append(seq, genOp(rng))
	}
	store1, store2 := secret.New(), secret.New()
	svc1, svc2 := New(store1), New(store2)
	n1, n2 := newNaive(), newNaive()
	var ids1, ids2 []int
	for i, o := range seq {
		r1 := applyReal(store1, svc1, o, ids1)
		r2 := applyReal(store2, svc2, o, ids2)
		m1 := applyNaive(n1, o, ids1)
		m2 := applyNaive(n2, o, ids2)
		if r1 != r2 || m1 != m2 || r1 != m1 {
			t.Fatalf("op%02d %s nondeterministic: %s | %s | %s | %s", i, opString(o), r1, r2, m1, m2)
		}
		if o.kind == opInject && len(r1) > 7 && r1[:7] == "Inject#" {
			var id int
			fmt.Sscanf(r1, "Inject#%d", &id)
			ids1 = append(ids1, id)
			ids2 = append(ids2, id)
		}
	}
}
