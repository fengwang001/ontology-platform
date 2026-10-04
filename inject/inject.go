package inject

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"ontology/scope"
	"ontology/secret"
)

var (
	// ErrInvalidArgument, ErrNotFound and ErrAlreadyExists mirror the store errors.
	ErrInvalidArgument = secret.ErrInvalidArgument
	ErrNotFound        = secret.ErrNotFound
	ErrAlreadyExists   = secret.ErrAlreadyExists

	// ErrMissingSecret means no definition exists for a required name.
	ErrMissingSecret = errors.New("required secret not found")
	ErrFork          = errors.New("secret withheld: pull request from fork")
	ErrProtected     = errors.New("secret withheld: protected branch only")
	// ErrEnvNotAllowed means the job ref is not allowed by the environment.
	ErrEnvNotAllowed = errors.New("environment does not allow this branch")
)

// Event is the job triggering event.
type Event string

const (
	Push       Event = "push"
	PRInternal Event = "pr_internal"
	PRFork     Event = "pr_fork"
)

// Want is a requested secret name and whether it is required.
type Want struct {
	Name     string
	Required bool
}

// Job is a pipeline job requesting secrets.
type Job struct {
	Repo  string
	Ref   string
	Event Event
	Env   string
	Wants []Want
}

// Status of a per-name resolution.
type Status string

const (
	StatusFound    Status = "found"
	StatusNotFound Status = "not_found"
	StatusWithheld Status = "withheld"
)

// Reason is why a secret was withheld. It is zero for found/not-found.
type Reason string

const (
	ReasonFork      Reason = "fork"
	ReasonProtected Reason = "protected"
)

// Result is the per-name resolution outcome.
type Result struct {
	Name    string
	Status  Status
	Reason  Reason
	Value   string
	Level   secret.Level
	Version int
}

// InjectError explains why a required secret made the whole Inject fail.
type InjectError struct {
	Name   string
	Status Status
	Reason Reason
}

func (e *InjectError) Error() string {
	switch e.Status {
	case StatusNotFound:
		return "inject: required secret not found: " + e.Name
	case StatusWithheld:
		return "inject: required secret withheld (" + string(e.Reason) + "): " + e.Name
	}
	return "inject: required secret unavailable: " + e.Name
}

func (e *InjectError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Status {
	case StatusNotFound:
		return ErrMissingSecret
	case StatusWithheld:
		if e.Reason == ReasonFork {
			return ErrFork
		}
		return ErrProtected
	}
	return nil
}

type jobRecord struct {
	values []string
}

// Service injects secret snapshots into jobs and masks log lines.
type Service struct {
	store *secret.Store

	mu     sync.Mutex
	nextID int
	jobs   map[int]*jobRecord

	// probes holds per-Inject lookup counts for the latest Inject calls;
	// exposed only inside this package for tests.
	probeMu sync.Mutex
	probes  []int
}

// NewService builds an injector over the given store.
func NewService(st *secret.Store) *Service {
	return &Service{store: st, jobs: map[int]*jobRecord{}}
}

func validEvent(ev Event) bool {
	return ev == Push || ev == PRInternal || ev == PRFork
}

func validateJob(job Job) error {
	if job.Repo == "" || job.Ref == "" || !validEvent(job.Event) {
		return ErrInvalidArgument
	}
	if len(job.Wants) < 1 || len(job.Wants) > 64 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, want := range job.Wants {
		if !secret.ValidName(want.Name) || seen[want.Name] {
			return ErrInvalidArgument
		}
		seen[want.Name] = true
	}
	return nil
}

// Inject resolves all requested names against one consistent snapshot.
// A required non-Found result fails the whole Inject and allocates no job id.
func (svc *Service) Inject(job Job) (int, []Result, error) {
	if err := validateJob(job); err != nil {
		return 0, nil, err
	}

	patterns, err := svc.store.RepoPatterns(job.Repo)
	if err != nil {
		return 0, nil, err
	}

	var envBranches []string
	if job.Env != "" {
		envBranches, err = svc.store.EnvBranches(job.Repo, job.Env)
		if err != nil {
			return 0, nil, err
		}
	}

	if job.Env != "" && len(envBranches) > 0 && !scope.MatchAny(envBranches, job.Ref) {
		return 0, nil, ErrEnvNotAllowed
	}

	protected := job.Event == Push && scope.MatchAny(patterns, job.Ref)

	results := make([]Result, len(job.Wants))
	snapshot := []string{}
	probeCount := 0

	if job.Event == PRFork {
		for i, want := range job.Wants {
			results[i] = Result{Name: want.Name, Status: StatusWithheld, Reason: ReasonFork}
		}
	} else {
		viewErr := svc.store.View(job.Repo, job.Env, func(rd *secret.Reader) error {
			for i, want := range job.Wants {
				entry, ok := rd.Lookup(want.Name)
				if !ok {
					results[i] = Result{Name: want.Name, Status: StatusNotFound}
					continue
				}
				if entry.ProtectedOnly && !protected {
					results[i] = Result{Name: want.Name, Status: StatusWithheld, Reason: ReasonProtected}
					continue
				}
				results[i] = Result{
					Name:    want.Name,
					Status:  StatusFound,
					Value:   entry.Value,
					Level:   entry.Level,
					Version: entry.Version,
				}
				snapshot = append(snapshot, entry.Value)
			}
			probeCount = rd.Probes()
			return nil
		})
		if viewErr != nil {
			return 0, nil, viewErr
		}
	}

	svc.recordProbes(probeCount)

	for i, want := range job.Wants {
		if want.Required && results[i].Status != StatusFound {
			res := results[i]
			return 0, results, &InjectError{Name: res.Name, Status: res.Status, Reason: res.Reason}
		}
	}

	svc.mu.Lock()
	svc.nextID++
	id := svc.nextID
	svc.jobs[id] = &jobRecord{values: snapshot}
	svc.mu.Unlock()

	return id, results, nil
}

func (svc *Service) recordProbes(n int) {
	svc.probeMu.Lock()
	svc.probes = append(svc.probes, n)
	svc.probeMu.Unlock()
}

func (svc *Service) lastProbes() int {
	svc.probeMu.Lock()
	defer svc.probeMu.Unlock()
	if len(svc.probes) == 0 {
		return 0
	}
	return svc.probes[len(svc.probes)-1]
}

// Mask replaces every maximal merged span covering occurrences of injected
// values (length >= 4) with "***". Unknown job ids report ErrNotFound.
func (svc *Service) Mask(jobID int, line string) (string, error) {
	svc.mu.Lock()
	rec, ok := svc.jobs[jobID]
	svc.mu.Unlock()
	if !ok {
		return "", ErrNotFound
	}
	values := append([]string(nil), rec.values...)

	type span struct{ start, end int }
	var spans []span
	for _, value := range values {
		if len(value) < 4 {
			continue
		}
		for start := 0; ; {
			idx := strings.Index(line[start:], value)
			if idx < 0 {
				break
			}
			from := start + idx
			spans = append(spans, span{from, from + len(value)})
			start = from + 1
			if start > len(line) {
				break
			}
		}
	}
	if len(spans) == 0 {
		return line, nil
	}

	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})

	merged := []span{{spans[0].start, spans[0].end}}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.start <= last.end {
			if sp.end > last.end {
				last.end = sp.end
			}
		} else {
			merged = append(merged, sp)
		}
	}

	var b strings.Builder
	prev := 0
	for _, sp := range merged {
		b.WriteString(line[prev:sp.start])
		b.WriteString("***")
		prev = sp.end
	}
	b.WriteString(line[prev:])
	return b.String(), nil
}
