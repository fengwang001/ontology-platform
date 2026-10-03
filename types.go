package ontology

import (
	"container/list"
	"errors"
	"sync"
)

const (
	StatusPending     = "pending"
	StatusValid       = "valid"
	StatusInvalid     = "invalid"
	StatusDeactivated = "deactivated"
	StatusExpired     = "expired"
	StatusReady       = "ready"
	StatusActive      = "active"
)

const (
	KindInvalidArgument = "invalid_argument"
	KindClockRollback   = "clock_rollback"
	KindBadNonce        = "bad_nonce"
	KindNotFound        = "not_found"
	KindConflict        = "conflict"
	KindCSRMismatch     = "csr_mismatch"
	KindRateLimited     = "rate_limited"
	KindQuotaExceeded   = "quota_exceeded"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrBadNonce        = errors.New("bad nonce")
	ErrNotFound        = errors.New("object not found")
	ErrConflict        = errors.New("status conflict")
	ErrCSRMismatch     = errors.New("CSR identifier set mismatch")
	ErrRateLimited     = errors.New("rate limited")
	ErrQuotaExceeded   = errors.New("pending authorization quota exceeded")
)

type Config struct {
	AuthPendingTTL   int64
	AuthValidTTL     int64
	OrderTTL         int64
	FailureWindow    int64
	FailureThreshold int64
	NonceCapacity    int64
	PendingAuthLimit int64
}

type Authorization struct {
	ID         string
	Account    []byte
	Identifier string
	Status     string
	Expires    int64
}

type Order struct {
	ID               string
	Account          []byte
	Identifiers      []string
	AuthorizationIDs []string
	Expires          int64
	Status           string
	CertSerial       int64
}

type StatusResult struct {
	Order  *Order
	Status string
}

type Error struct {
	Kind       string
	Current    string
	Identifier string
	Count      int
	P          int
	Q          int
	err        error
}

func (e *Error) Error() string {
	if e == nil || e.err == nil {
		return "ontology error"
	}
	return e.err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type authRecord struct {
	id         int
	account    string
	identifier string
	status     string
	expires    int64
}

type orderRecord struct {
	id          int
	account     string
	identifiers []string
	authIDs     []int
	expires     int64
	status      string
	certSerial  int64
}

type failureQueue struct {
	times []int64
	head  int
}

type StateMachine struct {
	mu sync.Mutex

	cfg Config

	clock    int64
	clockSet bool

	auths     []*authRecord
	orders    []*orderRecord
	certCount int64

	nonceCounter  uint64
	nonces        map[uint64]struct{}
	nonceOrder    *list.List
	nonceElements map[uint64]*list.Element

	validIndexes map[string]map[string]*reuseIndex
	pendingSet   map[string]map[int]struct{}
	failures     map[string]map[string]*failureQueue

	lookupExamined int64
}
