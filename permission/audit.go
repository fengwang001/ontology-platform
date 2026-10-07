package permission

import (
	"fmt"
	"strings"
	"time"
)

func (s *Store) AuditLog() []AuditRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]AuditRecord, len(s.audit))
	copy(records, s.audit)
	return records
}

func (s *Store) appendAudit(call, input, output, basis string) {
	s.audit = append(s.audit, AuditRecord{Call: call, Input: input, Output: output, TemporalBasis: basis})
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatChange(change Change) string {
	return fmt.Sprintf("id=%s subject=%s label=%s kind=%s decision=%s revokes=%s submitted=%s effective=%s",
		change.ID, change.Subject, change.Label, change.Kind, change.Decision, change.RevokesID,
		formatTime(change.SubmittedAt), formatTime(change.EffectiveAt))
}

func formatWithdraw(request WithdrawRequest) string {
	return fmt.Sprintf("id=%s withdrawn=%s", request.ID, formatTime(request.WithdrawnAt))
}

func formatQuery(query Query) string {
	return fmt.Sprintf("subject=%s label=%s at=%s", query.Subject, query.Label, formatTime(query.At))
}

func formatSubmitResult(result SubmitResult) string {
	return "accepted=true order=" + result.OrderKey
}

func formatQueryResult(result QueryResult) string {
	return fmt.Sprintf("decision=%s winner=%s examined=%s", result.Decision, result.WinningChangeID,
		strings.Join(result.ExaminedChangeID, ","))
}
