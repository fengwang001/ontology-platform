package seal

import (
	"strings"
	"sync"
	"time"
)

// Service is the seal usage-control service. It is safe for concurrent use:
// every operation is linearized under one mutex, so concurrent operations are
// equivalent to some serial order and identical replays yield identical results.
type Service struct {
	mu      sync.Mutex
	policy  Policy
	lastNow int64
	seals   map[string]*Seal
	grants  map[string]*Grant
	apps    map[string]*Application
	// daily counts: grant id -> day index (floor(t/86400)) -> success count.
	daily map[string]map[int64]int
	// freeze bookkeeping per employee:
	//   overrun heap ordered by deadline; the count of uncleared overruns
	//   equals the number of un-receipted executed applications past deadline.
	overruns map[string][]int64
	frozen   map[string]bool
	log      Logger
	seq      int
}

// NewService creates a service with the given policy. Panics on an inconsistent
// policy (construction-time configuration error, not a runtime operation).
func NewService(policy Policy, log Logger) *Service {
	if policy.Tier1 <= 0 || policy.Tier2 <= policy.Tier1 ||
		policy.ApprovalValidity <= 0 || policy.ReceiptWindow <= 0 {
		panic("seal: invalid policy")
	}
	if log == nil {
		log = nopLogger{}
	}
	return &Service{
		policy:   policy,
		seals:    map[string]*Seal{},
		grants:   map[string]*Grant{},
		apps:     map[string]*Application{},
		daily:    map[string]map[int64]int{},
		overruns: map[string][]int64{},
		frozen:   map[string]bool{},
		log:      log,
	}
}

type nopLogger struct{}

func (nopLogger) Log(LogEntry) {}

// SliceLogger keeps every entry in process memory (handy for tests/replay).
type SliceLogger struct {
	mu      sync.Mutex
	Entries []LogEntry
}

func (l *SliceLogger) Log(e LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Entries = append(l.Entries, e)
}

// SnapshotLogger logs with a timestamp tag; unused generic helper retained for docs.
type SnapshotLogger struct{ Inner Logger }

func (l SnapshotLogger) Log(e LogEntry) {
	if e.Now == 0 {
		e.Now = time.Now().Unix()
	}
	l.Inner.Log(e)
}

// ---- validation helpers (skeleton) ----

func badID(id string) bool { return id == "" || strings.ContainsAny(id, " \t\n") }

// ---- application lifecycle (skeleton) ----

// Domain logic lives in admin.go, approval.go, execute.go, receipt.go and
// query.go.
