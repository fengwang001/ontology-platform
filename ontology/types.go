package ontology

import "fmt"

type Bytes []byte

type Certificate struct {
	ID        Bytes
	Subject   Bytes
	Issuer    Bytes
	Key       Bytes
	AuthKey   Bytes
	NotBefore int64
	NotAfter  int64
	IsCA      bool
	PathLen   int
	Permitted []string
	Excluded  []string
	SAN       []string
}

type RejectKind string

const (
	RejectInvalidArgument RejectKind = "invalid_argument"
	RejectNotFound        RejectKind = "not_found"
	RejectConflict        RejectKind = "conflict"
	RejectLimitExceeded   RejectKind = "limit_exceeded"
)

type RejectError struct {
	Kind    RejectKind
	Field   string
	Index   int
	Message string
}

func (e *RejectError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Field)
	}
	return string(e.Kind)
}

type FailureReason string

const (
	FailureNotYetValid     FailureReason = "not_yet_valid"
	FailureExpired         FailureReason = "expired"
	FailureRevoked         FailureReason = "revoked"
	FailureNotCA           FailureReason = "not_ca"
	FailurePathLenExceeded FailureReason = "path_len_exceeded"
	FailureExcludedDNS     FailureReason = "excluded_dns"
	FailurePermittedDNS    FailureReason = "permitted_dns"
	FailureSANMismatch     FailureReason = "san_mismatch"
)

type VerifyFailure struct {
	Reason  FailureReason
	CertIdx int
	Path    []Bytes
}

type VerifyResult struct {
	Path       []Bytes
	Failure    *VerifyFailure
	NoPath     bool
	candidates int
}

func (r VerifyResult) ExaminedCandidates() int {
	return r.candidates
}
