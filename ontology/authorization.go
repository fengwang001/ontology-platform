package ontology

import "errors"

var (
	ErrInvalidPath      = errors.New("invalid resource path")
	ErrEmptySubject     = errors.New("empty subject")
	ErrEmptyAction      = errors.New("empty action")
	ErrInvalidEffect    = errors.New("invalid effect")
	ErrRuleLimitReached = errors.New("rule limit reached")
	ErrRuleNotFound     = errors.New("rule not found")
)

type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

type Rule struct {
	Path    string
	Subject string
	Action  string
	Effect  Effect
}

type Decision struct {
	Effect     Effect
	BasisPath  string
	HasNoBasis bool
}

type CacheStats struct {
	Hits         int64
	Computations int64
	Invalidated  int64
}
