package overload

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

type TypeID string

type Rank uint8

const (
	RankNone Rank = iota
	RankIdentity
	RankPromotion
	RankUser
)

type Param struct {
	Type     TypeID
	Default  bool
	Variadic bool
}

type Declaration struct {
	Name   string
	Params []Param
}

type Call struct {
	Name string
	Args []TypeID
}

type RejectReason string

const (
	RejectTooFewArgs     RejectReason = "too_few_arguments"
	RejectTooManyArgs    RejectReason = "too_many_arguments"
	RejectUnconvertible  RejectReason = "unconvertible_argument"
	RejectChainedUserDef RejectReason = "chained_user_defined_conversion"
)

type RegistrationErrorKind string

const (
	ErrUndefinedType    RegistrationErrorKind = "undefined_type"
	ErrIllegalSignature RegistrationErrorKind = "illegal_signature"
	ErrDuplicate        RegistrationErrorKind = "duplicate_declaration"
	ErrPromotionCycle   RegistrationErrorKind = "promotion_cycle"
)

type RegistrationError struct {
	Kind RegistrationErrorKind
	Msg  string
}

func (e *RegistrationError) Error() string {
	return string(e.Kind) + ": " + e.Msg
}

type TieRule uint8

const (
	TieNone TieRule = iota
	TieNoVariadic
	TieFewerDefaults
	TieMoreSpecialized
)

type PositionRank struct {
	Position         int
	From             TypeID
	To               TypeID
	Rank             Rank
	PromotionsBefore uint8
	PromotionsAfter  uint8
	UsesUserDef      bool
	ChainedUserDef   bool
}

type CandidateRejection struct {
	Reason   RejectReason
	Message  string
	Position int
	From     TypeID
	To       TypeID
}

type CandidateReport struct {
	Declaration        Declaration
	Applicable         bool
	Rejection          CandidateRejection
	Conversions        []PositionRank
	UsedVariadic       bool
	DefaultsUsed       int
	SpecializationWins []int
}

type Report struct {
	Call                Call
	Selected            *Declaration
	Ambiguous           bool
	AmbiguousCandidates []Declaration
	NoMatch             bool
	Candidates          []CandidateReport
	TieRule             TieRule
	Basis               string
}

type Logger interface {
	Printf(format string, args ...any)
}

type stdLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *stdLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format+"\n", args...)
}

type snapshot struct {
	types        map[TypeID]struct{}
	declarations map[string][]Declaration
	conv         conversionTable
}

type Registry struct {
	mu     sync.RWMutex
	snap   snapshot
	logger Logger
}

func NewRegistry(options ...func(*Registry)) *Registry {
	r := &Registry{snap: newSnapshot(), logger: defaultLogger()}
	for _, option := range options {
		option(r)
	}
	return r
}

func WithLogger(l Logger) func(*Registry) {
	return func(r *Registry) { r.logger = l }
}

func WithLogWriter(w io.Writer) func(*Registry) {
	return WithLogger(&stdLogger{w: w})
}

func (r *Registry) DefineType(t TypeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snap.clone()
	s.types[t] = struct{}{}
	r.snap = s
	r.log("DEFINE_TYPE name=%q", t)
	return nil
}

func (r *Registry) AddDeclaration(d Declaration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snap.clone()
	if err := validateDeclaration(s, d); err != nil {
		r.log("ADD_DECL name=%q rejected=%v", d.Name, err)
		return err
	}
	s.declarations[d.Name] = insertSortedDeclaration(s.declarations[d.Name], d)
	r.snap = s
	r.log("ADD_DECL name=%q params=%d", d.Name, len(d.Params))
	return nil
}

func (r *Registry) AddPromotion(from, to TypeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snap.clone()
	if _, ok := s.types[from]; !ok {
		return undefinedType(from)
	}
	if _, ok := s.types[to]; !ok {
		return undefinedType(to)
	}
	if err := s.conv.addPromotion(from, to); err != nil {
		r.log("ADD_PROMOTION from=%q to=%q rejected=%v", from, to, err)
		return err
	}
	r.snap = s
	r.log("ADD_PROMOTION from=%q to=%q", from, to)
	return nil
}

func (r *Registry) AddUserConversion(from, to TypeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.snap.clone()
	if _, ok := s.types[from]; !ok {
		return undefinedType(from)
	}
	if _, ok := s.types[to]; !ok {
		return undefinedType(to)
	}
	s.conv.addUserConversion(from, to)
	r.snap = s
	r.log("ADD_USER_CONVERSION from=%q to=%q", from, to)
	return nil
}

func (r *Registry) Resolve(callName string, call Call) Report {
	r.mu.RLock()
	s := r.snap
	r.mu.RUnlock()
	if call.Name == "" {
		call.Name = callName
	}
	report := resolveSnapshot(s, call)
	r.log("RESOLVE call=%s args=%s selected=%s ambiguous=%t no_match=%t tie=%d basis=%q",
		call.Name, joinTypes(call.Args), selectedName(report.Selected), report.Ambiguous, report.NoMatch, report.TieRule, report.Basis)
	return report
}

func (r *Registry) log(format string, args ...any) {
	if r.logger != nil {
		r.logger.Printf(format, args...)
	}
}

func newSnapshot() snapshot {
	return snapshot{
		types:        map[TypeID]struct{}{},
		declarations: map[string][]Declaration{},
		conv:         newConversionTable(),
	}
}

func (s snapshot) clone() snapshot {
	types := make(map[TypeID]struct{}, len(s.types))
	for t := range s.types {
		types[t] = struct{}{}
	}
	declarations := make(map[string][]Declaration, len(s.declarations))
	for name, ds := range s.declarations {
		cp := make([]Declaration, len(ds))
		copy(cp, ds)
		declarations[name] = cp
	}
	return snapshot{types: types, declarations: declarations, conv: s.conv.clone()}
}

func validateDeclaration(s snapshot, d Declaration) error {
	for _, param := range d.Params {
		if _, ok := s.types[param.Type]; !ok {
			return undefinedType(param.Type)
		}
	}
	sawDefault := false
	for i, param := range d.Params {
		if param.Default {
			sawDefault = true
		} else if sawDefault {
			return &RegistrationError{Kind: ErrIllegalSignature, Msg: "non-default parameter follows a default parameter"}
		}
		if param.Variadic && i != len(d.Params)-1 {
			return &RegistrationError{Kind: ErrIllegalSignature, Msg: "variadic parameter must be last"}
		}
	}
	key := signatureKey(d)
	for _, existing := range s.declarations[d.Name] {
		if signatureKey(existing) == key {
			return &RegistrationError{Kind: ErrDuplicate, Msg: "declaration with same parameter types and variadic marker already exists"}
		}
	}
	return nil
}

func signatureKey(d Declaration) string {
	var b strings.Builder
	for _, param := range d.Params {
		b.WriteString(string(param.Type))
		b.WriteByte(0)
		if param.Variadic {
			b.WriteByte('v')
		}
		b.WriteByte(';')
	}
	return b.String()
}

func insertSortedDeclaration(ds []Declaration, d Declaration) []Declaration {
	key := signatureKey(d)
	index := sort.Search(len(ds), func(i int) bool { return signatureKey(ds[i]) >= key })
	ds = append(ds, Declaration{})
	copy(ds[index+1:], ds[index:])
	ds[index] = d
	return ds
}

func undefinedType(t TypeID) error {
	return &RegistrationError{Kind: ErrUndefinedType, Msg: "type " + string(t) + " is not defined"}
}

func joinTypes(types []TypeID) string {
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = string(t)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func selectedName(d *Declaration) string {
	if d == nil {
		return "-"
	}
	return d.Name
}

func defaultLogger() Logger { return &stdLogger{w: os.Stderr} }
