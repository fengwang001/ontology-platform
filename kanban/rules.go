package kanban

import "strings"

func nonEmpty(value string) bool {
	return strings.TrimSpace(value) != ""
}

func validateBoardConfig(id string, config BoardConfig) error {
	if !nonEmpty(id) || len(config.Columns) < 3 || len(config.Columns) > 8 || config.AssigneeLimit < 1 || config.AssigneeLimit > 50 {
		return ErrInvalidArgument
	}
	for index, column := range config.Columns {
		if !nonEmpty(column.Name) || column.Limit < 0 {
			return ErrInvalidArgument
		}
		if index != len(config.Columns)-1 && column.Limit == 0 {
			// Zero intentionally means unlimited for non-done columns.
		}
	}
	return nil
}

func validVersionedRequest(boardID, user, cardID string, version, now int64) bool {
	return nonEmpty(boardID) && nonEmpty(user) && nonEmpty(cardID) && version > 0 && now >= 0 && now <= 1_000_000_000_000
}

func validateMoveTransition(from, to, done int) error {
	if from == done {
		return ErrIllegalTransition
	}
	if to == from {
		return ErrIllegalTransition
	}
	if to > from {
		if to != from+1 {
			return ErrIllegalTransition
		}
	} else if to < 0 {
		return ErrIllegalTransition
	}
	return nil
}

func (b *board) columnFits(column int, expedited bool) bool {
	if expedited || column == b.doneColumn() {
		return true
	}
	limit := b.config.Columns[column].Limit
	return limit == 0 || b.columnOccupancy[column] < limit
}

func (b *board) assigneeFits(assignee string, from, to int, expedited bool) bool {
	if expedited || !b.isProgress(to) {
		return true
	}
	next := b.assigneeWIP[assignee]
	if b.isProgress(from) {
		next--
	}
	next++
	return next <= b.config.AssigneeLimit
}

func (b *board) checkCapacity(card *cardState, to int, assignee string, expedited bool) (string, error) {
	if !b.columnFits(to, expedited) {
		return "target column occupancy would exceed its limit", ErrColumnLimitFull
	}

	next := b.assigneeWIP[assignee]
	if b.isProgress(card.column) && assignee == card.assignee {
		next--
	}
	if b.isProgress(to) {
		next++
	}
	if !expedited && b.isProgress(to) && next > b.config.AssigneeLimit {
		return "assignee work-in-progress would exceed G", ErrAssigneeLimitFull
	}
	return "", nil
}

func (b *board) planExpedite(card *cardState, to int, requested bool) (bool, string, error) {
	if !b.isProgress(to) {
		return false, "expedite clears outside progress", nil
	}
	if requested && b.expeditedCardID != "" && b.expeditedCardID != card.id {
		return false, "another expedited card is already in progress", ErrExpediteOccupied
	}
	if requested || (card.expedited && b.isProgress(card.column)) {
		return true, "expedite capacity exemption accepted", nil
	}
	return false, "normal capacity limits apply", nil
}

func (b *board) blockedByPrerequisite(cardID string) (string, error) {
	incomplete := b.dependencies.incompletePrerequisites(cardID, func(prerequisiteID string) bool {
		prerequisite, ok := b.cards[prerequisiteID]
		return ok && prerequisite.column == b.doneColumn()
	})
	if len(incomplete) > 0 {
		return "at least one prerequisite is not in the done column", ErrDependencyBlocked
	}
	return "", nil
}

func (b *board) blockedByDependent(cardID string) (string, error) {
	blocked := b.dependencies.hasActiveDependent(cardID, func(dependentID string) bool {
		dependent, ok := b.cards[dependentID]
		return ok && dependent.column != b.todoColumn()
	})
	if blocked {
		return "a dependent card is in progress or done", ErrDependencyBlocked
	}
	return "", nil
}
