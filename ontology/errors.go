package ontology

import (
	"fmt"
	"strconv"
)

// Error kinds. Rejected operations are reported in this priority order:
// parameter invalid -> clock regression -> nonce invalid -> not found ->
// state conflict -> CSR mismatch -> rate limited -> quota exceeded.
const (
	KindConfig      = "config_invalid"
	KindParam       = "param_invalid"
	KindClock       = "clock_regression"
	KindNonce       = "nonce_invalid"
	KindNotFound    = "not_found"
	KindState       = "state_conflict"
	KindCSR         = "csr_mismatch"
	KindRateLimited = "rate_limited"
	KindQuota       = "quota_exceeded"
)

// Error carries a machine-readable Kind and locating context.
type Error struct {
	Kind   string
	Reason string
	// Context fields, zero-valued when irrelevant.
	Nonce   int64
	Ref     string // order or authorization id
	Ident   string // identifier for rate limiting
	Count   int    // number of in-window failures
	Pending int    // p: current pending authorizations
	Need    int    // q: authorizations to create
	Status  string // current effective status on state conflicts
}

func (e *Error) Error() string {
	s := e.Kind + ": " + e.Reason
	if e.Ident != "" {
		s += " ident=" + e.Ident + " count=" + strconv.Itoa(e.Count)
	}
	if e.Kind == KindQuota {
		s += " p=" + strconv.Itoa(e.Pending) + " q=" + strconv.Itoa(e.Need)
	}
	if e.Status != "" {
		s += " status=" + e.Status
	}
	return s
}

func errf(kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Reason: fmt.Sprintf(format, args...)}
}

// validIdent reports whether s is a non-empty lowercase identifier:
// letters, digits, '-' and '.', optionally with a single leading "*.".
func validIdent(s string) bool {
	if len(s) == 0 {
		return false
	}
	body := s
	if len(s) >= 2 && s[0] == '*' && s[1] == '.' {
		body = s[2:]
	}
	if len(body) == 0 {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// validateIdents checks the 1..10 distinct valid identifier list rule.
func validateIdents(idents []string) *Error {
	if len(idents) < 1 || len(idents) > 10 {
		return errf(KindParam, "identifier count must be in [1,10], got %d", len(idents))
	}
	seen := make(map[string]struct{}, len(idents))
	for _, id := range idents {
		if !validIdent(id) {
			return errf(KindParam, "illegal identifier %q", id)
		}
		if _, dup := seen[id]; dup {
			return errf(KindParam, "duplicate identifier %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateCSR checks that every CSR identifier is valid and distinct.
// Set equality with the order identifiers is a CSR-mismatch, not a parameter
// error, and is checked later against the order.
func validateCSR(idents []string) *Error {
	seen := make(map[string]struct{}, len(idents))
	for _, id := range idents {
		if !validIdent(id) {
			return errf(KindParam, "illegal CSR identifier %q", id)
		}
		if _, dup := seen[id]; dup {
			return errf(KindParam, "duplicate CSR identifier %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
