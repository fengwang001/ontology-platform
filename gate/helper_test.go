package gate_test

import (
	"errors"
	"sort"
	"strings"

	"ontology/gate"
	"ontology/owners"
	"ontology/review"
)

type vkey struct {
	pr   int
	user string
}

type ckey struct {
	pr    int
	check string
	head  int
}

type simPR struct {
	id     int
	author string
	files  []string
	draft  bool
	merged bool
	base   int
	head   int
}

// simulator 是按题面规则独立重写的朴素实现。
type simulator struct {
	cfg     gate.Config
	rules   []owners.Rule
	perms   map[string]review.Level
	prs     map[int]*simPR
	verdict map[vkey]review.Verdict
	checks  map[ckey]review.Status
	nextID  int
	t       int
}

func newSimulator(cfg gate.Config, rules []owners.Rule) *simulator {
	return &simulator{
		cfg:     cfg,
		rules:   rules,
		perms:   map[string]review.Level{},
		prs:     map[int]*simPR{},
		verdict: map[vkey]review.Verdict{},
		checks:  map[ckey]review.Status{},
	}
}

func (s *simulator) ownerOf(path string) []string {
	var out []string
	found := false
	for _, r := range s.rules {
		p := r.Pattern
		ok := false
		switch {
		case p == "*":
			ok = true
		case strings.HasSuffix(p, "/"):
			ok = strings.HasPrefix(path, p)
		case strings.HasPrefix(p, "*."):
			ok = len(path) > len(p)-1 && strings.HasSuffix(path, p[1:])
		default:
			ok = path == p
		}
		if ok {
			found = true
			out = r.Owners
		}
	}
	if !found || len(out) == 0 {
		return nil
	}
	return out
}

func (s *simulator) level(u string) review.Level {
	if l, ok := s.perms[u]; ok {
		return l
	}
	return review.None
}

func validFileList(files []string) bool {
	if len(files) < 1 || len(files) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, f := range files {
		if f == "" || seen[f] {
			return false
		}
		seen[f] = true
	}
	return true
}

func (s *simulator) setPerm(u string, lv review.Level) error {
	if u == "" || lv < review.None || lv > review.Admin {
		return gate.ErrInvalid
	}
	s.perms[u] = lv
	return nil
}

func (s *simulator) open(author string, files []string, draft bool) (int, error) {
	if author == "" || !validFileList(files) {
		return 0, gate.ErrInvalid
	}
	s.nextID++
	p := &simPR{id: s.nextID, author: author, files: append([]string(nil), files...), draft: draft, base: s.t, head: 1}
	s.prs[p.id] = p
	return p.id, nil
}

func (s *simulator) push(id int, files []string) error {
	if !validFileList(files) {
		return gate.ErrInvalid
	}
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if p.merged {
		return gate.ErrState
	}
	p.head++
	p.files = append([]string(nil), files...)
	if s.cfg.DismissStale {
		for k, v := range s.verdict {
			if k.pr == id && v == review.Approve {
				delete(s.verdict, k)
			}
		}
	}
	return nil
}

func (s *simulator) ready(id int) error {
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if p.merged || !p.draft {
		return gate.ErrState
	}
	p.draft = false
	return nil
}

func (s *simulator) updateBranch(id int) error {
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if p.merged || p.base == s.t {
		return gate.ErrState
	}
	p.base = s.t
	p.head++
	return nil
}

func (s *simulator) review(id int, u string, v review.Verdict) error {
	if u == "" || v < review.Approve || v > review.Comment {
		return gate.ErrInvalid
	}
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if p.merged {
		return gate.ErrState
	}
	if v != review.Comment && u == p.author {
		return gate.ErrSelfReview
	}
	if v != review.Comment {
		s.verdict[vkey{id, u}] = v
	}
	return nil
}

func (s *simulator) dismiss(id int, reviewer, actor string) error {
	if reviewer == "" || actor == "" {
		return gate.ErrInvalid
	}
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if s.level(actor) != review.Admin {
		return gate.ErrForbidden
	}
	if p.merged {
		return gate.ErrState
	}
	k := vkey{id, reviewer}
	if _, ok := s.verdict[k]; !ok {
		return gate.ErrState
	}
	delete(s.verdict, k)
	return nil
}

func (s *simulator) report(id int, check string, head int, st review.Status) error {
	if check == "" || head < 1 || st < review.Pending || st > review.Skipped {
		return gate.ErrInvalid
	}
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if p.merged || head > p.head {
		return gate.ErrState
	}
	s.checks[ckey{id, check, head}] = st
	return nil
}

func (s *simulator) active(id int, v review.Verdict) map[string]bool {
	out := map[string]bool{}
	for k, got := range s.verdict {
		if k.pr == id && got == v {
			lv := s.level(k.user)
			if lv == review.Write || lv == review.Admin {
				out[k.user] = true
			}
		}
	}
	return out
}

func (s *simulator) mergeable(id int) (error, string) {
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound, ""
	}
	switch {
	case p.merged:
		return gate.ErrMerged, ""
	case p.draft:
		return gate.ErrDraft, ""
	}
	appr := s.active(id, review.Approve)
	cr := s.active(id, review.RequestChanges)
	if len(cr) > 0 {
		return gate.ErrChangeRequest, ""
	}
	if len(appr) < s.cfg.N {
		return gate.ErrApprovals, ""
	}
	if s.cfg.RequireOwners {
		var missing []string
		for _, f := range p.files {
			var eligible []string
			for _, o := range s.ownerOf(f) {
				if o != p.author {
					eligible = append(eligible, o)
				}
			}
			if len(eligible) == 0 {
				continue
			}
			covered := false
			for _, o := range eligible {
				if appr[o] {
					covered = true
					break
				}
			}
			if !covered {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return gate.ErrOwnerApproval, missing[0]
		}
	}
	for _, c := range s.cfg.Required {
		if s.checks[ckey{id, c, p.head}] == review.Failure {
			return gate.ErrCheckFailure, c
		}
	}
	for _, c := range s.cfg.Required {
		st, ok := s.checks[ckey{id, c, p.head}]
		if !ok || st == review.Pending || st == review.Failure {
			return gate.ErrCheckPending, c
		}
	}
	if s.cfg.Strict && p.base < s.t {
		return gate.ErrStaleBase, ""
	}
	return nil, ""
}

func (s *simulator) merge(id int, actor string) error {
	if actor == "" {
		return gate.ErrInvalid
	}
	p, ok := s.prs[id]
	if !ok {
		return gate.ErrNotFound
	}
	if lv := s.level(actor); lv != review.Write && lv != review.Admin {
		return gate.ErrForbidden
	}
	if reason, _ := s.mergeable(id); reason != nil {
		return reason
	}
	p.merged = true
	s.t++
	return nil
}

var sentinels = []error{
	gate.ErrMerged, gate.ErrDraft, gate.ErrChangeRequest, gate.ErrApprovals,
	gate.ErrOwnerApproval, gate.ErrCheckFailure, gate.ErrCheckPending, gate.ErrStaleBase,
	gate.ErrNotFound, gate.ErrInvalid, gate.ErrForbidden, gate.ErrState, gate.ErrSelfReview,
}

func reasonKey(err error) string {
	if err == nil {
		return "<nil>"
	}
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return err.Error()
}
