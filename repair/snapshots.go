package repair

import "strings"

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func hasBlank(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func setValues(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func (s *Service) snapshotTicket(work *ticket) Ticket {
	rejected := make([]int64, 0, len(work.rejectedBy))
	for id := range work.rejectedBy {
		rejected = append(rejected, id)
	}
	return Ticket{
		ID:             work.id,
		TenantID:       work.tenantID,
		Trade:          work.trade,
		Building:       work.building,
		Level:          work.level,
		SubmittedAt:    work.submittedAt,
		LevelStartedAt: work.levelStartedAt,
		Status:         work.status,
		Assignee:       work.assignee,
		DispatchedAt:   work.dispatchedAt,
		ConfirmedAt:    work.confirmedAt,
		ResponseDue:    work.responseDue,
		CompleteDue:    work.completeDue,
		Rejections:     work.rejections,
		RejectedBy:     rejected,
	}
}

func snapshotContractor(worker *contractor) Contractor {
	return Contractor{
		ID:              worker.id,
		Trades:          worker.trades,
		Buildings:       worker.buildings,
		Capacity:        worker.capacity,
		AcceptsUrgent:   worker.acceptsUrgent,
		Inactive:        worker.inactive,
		ActiveCount:     len(worker.active),
		LastCompletedAt: worker.lastCompletedAt,
	}
}

func snapshotEvent(value event) Event {
	return Event{
		At:           value.at,
		TicketID:     value.ticketID,
		ContractorID: value.contractorID,
		Kind:         value.kind,
		FromLevel:    value.fromLevel,
		ToLevel:      value.toLevel,
	}
}
