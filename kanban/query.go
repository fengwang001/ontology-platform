package kanban

func (s *Service) GetBoard(boardID string) (BoardSnapshot, error) {
	if !nonEmpty(boardID) {
		return BoardSnapshot{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[boardID]
	if !ok {
		return BoardSnapshot{}, ErrInvalidArgument
	}

	columns := append([]Column(nil), b.config.Columns...)
	cards := make(map[string]Card, len(b.cards))
	for id, card := range b.cards {
		cards[id] = b.snapshot(card)
	}
	occupancy := append([]int(nil), b.columnOccupancy...)
	assigneeWIP := make(map[string]int, len(b.assigneeWIP))
	for assignee, count := range b.assigneeWIP {
		assigneeWIP[assignee] = count
	}
	return BoardSnapshot{
		Config:          BoardConfig{Columns: columns, AssigneeLimit: b.config.AssigneeLimit},
		Cards:           cards,
		ColumnOccupancy: occupancy,
		AssigneeWIP:     assigneeWIP,
		ExpeditedCardID: b.expeditedCardID,
		LastNow:         b.lastNow,
	}, nil
}
