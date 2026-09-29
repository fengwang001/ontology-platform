package delegation

import (
	"context"
	"log"
	"os"
)

// StdLogger adapts the standard library logger to the delegation Logger
// interface. Every audit line contains delegator, delegatee, permission
// and the decision basis.
type StdLogger struct {
	l *log.Logger
}

// NewStdLogger writes audit events to stderr using the standard logger.
func NewStdLogger() *StdLogger {
	return &StdLogger{l: log.New(os.Stderr, "delegation ", log.LstdFlags|log.Lmicroseconds)}
}

// LogGrant records an accepted or rejected grant attempt.
func (s *StdLogger) LogGrant(ctx context.Context, d *Delegation, decision string, reason string) {
	s.l.Printf("grant delegator=%s delegatee=%s permission=%s can_delegate=%t expires_at=%s decision=%s basis=%s",
		d.Delegator, d.Delegatee, d.Permission, d.CanDelegate,
		d.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), decision, reason)
}

// LogRevoke records a cascading revocation (one line per invalidated edge).
func (s *StdLogger) LogRevoke(ctx context.Context, d *Delegation) {
	s.l.Printf("revoke id=%d delegator=%s delegatee=%s permission=%s basis=cascading_chain_invalidation",
		d.ID, d.Delegator, d.Delegatee, d.Permission)
}

// LogEvaluate records a permission decision and its basis.
func (s *StdLogger) LogEvaluate(ctx context.Context, subject Principal, perm Permission, allowed bool, reason string) {
	s.l.Printf("evaluate subject=%s permission=%s allowed=%t basis=%s", subject, perm, allowed, reason)
}
