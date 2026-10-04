package inject

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/scope"
	"ontology/secret"
)

var (
	ErrInvalid         = errors.New("invalid job")
	ErrRepoNotFound    = secret.ErrRepoNotFound
	ErrEnvNotFound     = secret.ErrEnvNotFound
	ErrEnvDenied       = errors.New("environment not allowed for ref")
	ErrJobNotFound     = errors.New("job not found")
	ErrNotFound        = errors.New("not found")
	ErrWithheldFork    = errors.New("withheld: fork pull request")
	ErrWithheldProtect = errors.New("withheld: protected branch only")
)

type Level int

const (
	LevelEnv Level = iota
	LevelRepo
	LevelOrg
)

type Want struct {
	Name     string
	Required bool
}

type Job struct {
	Repo  string
	Ref   string
	Event string
	Env   string
	Wants []Want
}

type Found struct {
	Value   string
	Level   Level
	Version int
}

type Result struct {
	// Found 非 nil 表示取到了定义；否则 Withheld 为扣留原因（nil 表示 NotFound）。
	Found    *Found
	Withheld error
}

type Outcome struct {
	JobID   int
	Results []Result
}

// Failure 是 required 名字未 Found 时整体失败携带的错误，可 errors.Is 到原因。
type Failure struct {
	Name   string
	Reason error
}

func (e *Failure) Error() string {
	return fmt.Sprintf("required secret %q unavailable: %v", e.Name, e.Reason)
}

func (e *Failure) Unwrap() error { return e.Reason }

type jobRecord struct {
	values []string
}

type Service struct {
	store *secret.Store
	mu    sync.Mutex
	jobs  map[int]jobRecord
	next  int
	// lastProbes 记录最近一次非 fork 注入的逐名存储探测总次数（非导出探针）。
	lastProbes int
}

func New(store *secret.Store) *Service { return &Service{store: store} }

func (svc *Service) Inject(job Job) (*Outcome, error) {
	if err := validateJob(job); err != nil {
		return nil, err
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()

	svc.lastProbes = 0
	view := svc.store.Snapshot()
	defer view.Release()

	repoView, ok := view.Repo(job.Repo)
	if !ok {
		return nil, ErrRepoNotFound
	}
	var envBranches []string
	if job.Env != "" {
		envBranches, ok = repoView.EnvBranches[job.Env]
		if !ok {
			return nil, ErrEnvNotFound
		}
		if len(envBranches) > 0 && !anyMatch(envBranches, job.Ref) {
			return nil, ErrEnvDenied
		}
	}

	results := make([]Result, len(job.Wants))
	var snapshot []string
	if job.Event == "pr_fork" {
		for i := range job.Wants {
			results[i] = Result{Withheld: ErrWithheldFork}
		}
		svc.lastProbes = 0
	} else {
		protected := scope.Protected(job.Event, job.Ref, repoView.Protected)
		probes := 0
		for i, want := range job.Wants {
			found, reason, n := resolveName(view, job.Repo, job.Env, want.Name, repoView.Private, protected)
			probes += n
			switch {
			case found != nil:
				results[i] = Result{Found: found}
				snapshot = append(snapshot, found.Value)
			case reason != nil:
				results[i] = Result{Withheld: reason}
			default:
				results[i] = Result{}
			}
		}
		svc.lastProbes = probes
	}
	for i, want := range job.Wants {
		if want.Required && results[i].Found == nil {
			reason := results[i].Withheld
			if reason == nil {
				reason = ErrNotFound
			}
			return nil, &Failure{Name: want.Name, Reason: reason}
		}
	}

	if svc.jobs == nil {
		svc.jobs = map[int]jobRecord{}
	}
	svc.next++
	id := svc.next
	svc.jobs[id] = jobRecord{values: snapshot}
	return &Outcome{JobID: id, Results: results}, nil
}

// probes 返回最近一次 Inject 的存储探测次数（非导出探针）。
func (svc *Service) probes() int { return svc.lastProbes }

func (svc *Service) Mask(jobID int, line string) (string, error) {
	svc.mu.Lock()
	rec, ok := svc.jobs[jobID]
	svc.mu.Unlock()
	if !ok {
		return "", ErrJobNotFound
	}
	return maskLine(line, rec.values), nil
}

func validateJob(job Job) error {
	if job.Repo == "" || job.Ref == "" {
		return ErrInvalid
	}
	switch job.Event {
	case "push", "pr_internal", "pr_fork":
	default:
		return ErrInvalid
	}
	if len(job.Wants) < 1 || len(job.Wants) > 64 {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(job.Wants))
	for _, w := range job.Wants {
		if !scope.ValidName(w.Name) {
			return ErrInvalid
		}
		if _, dup := seen[w.Name]; dup {
			return ErrInvalid
		}
		seen[w.Name] = struct{}{}
	}
	return nil
}

func anyMatch(patterns []string, ref string) bool {
	for _, p := range patterns {
		if scope.Match(p, ref) {
			return true
		}
	}
	return false
}

// resolveName 按 env→repo→org 次序解析单个名字，返回 (found, withheld, probes)。
// found 为 nil 且 withheld 为 nil 即 NotFound；probes 为本次存储探测次数。
func resolveName(view *secret.View, repo, env, name string, private, protected bool) (*Found, error, int) {
	probes := 0
	if env != "" {
		probes++
		if d, ok := view.LookupEnv(repo, env, name); ok {
			return materialize(d, LevelEnv, protected, probes)
		}
	}
	probes++
	if d, ok := view.LookupRepo(repo, name); ok {
		return materialize(d, LevelRepo, protected, probes)
	}
	probes++
	if d, ok := view.LookupOrg(name); ok {
		if !scope.Visible(d.Visibility, private, d.SelectedRepos, repo) {
			return nil, nil, probes
		}
		return materialize(d.Definition, LevelOrg, protected, probes)
	}
	return nil, nil, probes
}

func materialize(d secret.Definition, level Level, protected bool, probes int) (*Found, error, int) {
	if d.ProtectedOnly && !protected {
		return nil, ErrWithheldProtect, probes
	}
	return &Found{Value: d.Value, Level: level, Version: d.Version}, nil, probes
}

// maskLine 找出全部参与值（长度≥4）的全部出现区间，合并重叠/相接区间后整段替换为 ***。
func maskLine(line string, values []string) string {
	type interval struct{ start, end int }
	var intervals []interval
	seen := map[string]struct{}{}
	for _, v := range values {
		if len(v) < 4 {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		for start := 0; start+len(v) <= len(line); {
			idx := strings.Index(line[start:], v)
			if idx < 0 {
				break
			}
			idx += start
			intervals = append(intervals, interval{idx, idx + len(v)})
			start = idx + 1
		}
	}
	if len(intervals) == 0 {
		return line
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})

	merged := []interval{intervals[0]}
	for _, iv := range intervals[1:] {
		last := &merged[len(merged)-1]
		if iv.start <= last.end {
			if iv.end > last.end {
				last.end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}

	out := make([]byte, 0, len(line))
	cursor := 0
	for _, iv := range merged {
		out = append(out, line[cursor:iv.start]...)
		out = append(out, '*', '*', '*')
		cursor = iv.end
	}
	out = append(out, line[cursor:]...)
	return string(out)
}
