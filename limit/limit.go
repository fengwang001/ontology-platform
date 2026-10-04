package limit

import "errors"

var (
	ErrDuplicateAccount = errors.New("duplicate account")
	ErrUnknownAccount   = errors.New("unknown account")
)

type Side int

const (
	Long Side = iota + 1
	Short
)

type instrumentLimit struct {
	account int64
	group   int64
	day     int64
}

type Registry struct {
	groups map[string]string
	limits map[string]instrumentLimit
	hedges map[string]map[string][2]int64
}

func NewRegistry() *Registry {
	return &Registry{
		groups: make(map[string]string),
		limits: make(map[string]instrumentLimit),
		hedges: make(map[string]map[string][2]int64),
	}
}

func (r *Registry) Register(acct, group string) error {
	if _, ok := r.groups[acct]; ok {
		return ErrDuplicateAccount
	}
	r.groups[acct] = group
	return nil
}

func (r *Registry) Set(sym string, account, group, day int64) error {
	r.limits[sym] = instrumentLimit{account: account, group: group, day: day}
	return nil
}

func (r *Registry) SetHedge(acct, sym string, side Side, hedge int64) error {
	if _, ok := r.groups[acct]; !ok {
		return ErrUnknownAccount
	}
	bySymbol := r.hedges[acct]
	if bySymbol == nil {
		bySymbol = make(map[string][2]int64)
		r.hedges[acct] = bySymbol
	}
	values := bySymbol[sym]
	values[sideIndex(side)] = hedge
	bySymbol[sym] = values
	return nil
}

func (r *Registry) Group(acct string) (string, bool) {
	group, ok := r.groups[acct]
	return group, ok
}

func (r *Registry) Limits(sym string) (account, group, day int64, ok bool) {
	value, ok := r.limits[sym]
	return value.account, value.group, value.day, ok
}

func (r *Registry) Hedge(acct, sym string, side Side) int64 {
	return r.hedges[acct][sym][sideIndex(side)]
}

func sideIndex(side Side) int {
	if side == Short {
		return 1
	}
	return 0
}
