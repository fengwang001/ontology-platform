package hospital

import "sort"

func (s *System) RegisterRecall(input RecallInput) error {
	if err := nonEmpty(input.ID, "召回编号"); err != nil {
		return err
	}
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.First, "起始批号"); err != nil {
		return err
	}
	if err := nonEmpty(input.Last, "结束批号"); err != nil {
		return err
	}
	if input.First > input.Last {
		return invalidf("召回批号区间起点不能晚于终点")
	}
	if input.Level < 1 || input.Level > 3 {
		return invalidf("召回等级必须位于 1 到 3 之间")
	}
	if err := validNow(input.Now); err != nil {
		return err
	}
	if input.IssueAt < 0 || input.IssueAt > 1_000_000_000 {
		return invalidf("问题始发时刻必须位于 0 到 1000000000 之间")
	}
	if input.IssueAt > input.Now {
		return invalidf("问题始发时刻不能晚于 now")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	if _, exists := s.recalls[input.ID]; exists {
		return errorf(ErrInvalidState, "召回编号 %s 已存在", input.ID)
	}
	recall := &recallRecord{
		id:      input.ID,
		drug:    input.Drug,
		first:   input.First,
		last:    input.Last,
		level:   input.Level,
		issueAt: input.IssueAt,
		active:  true,
	}
	s.recalls[input.ID] = recall
	s.drug(input.Drug).activeRecalls[input.ID] = recall
	s.clock = input.Now
	return nil
}

func (s *System) CancelRecall(input CancelRecallInput) error {
	if err := nonEmpty(input.ID, "召回编号"); err != nil {
		return err
	}
	if err := validNow(input.Now); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	recall := s.recalls[input.ID]
	if recall == nil {
		return errorf(ErrNotFound, "召回 %s 不存在", input.ID)
	}
	if !recall.active {
		return errorf(ErrInvalidState, "召回 %s 已解除", input.ID)
	}
	delete(s.drug(recall.drug).activeRecalls, input.ID)
	recall.active = false
	s.clock = input.Now
	return nil
}

func recoveryItems(recall *recallRecord, state *drugState) []RecoveryItem {
	items := make(map[string]map[string]int)
	batchIDs := make([]string, 0)
	batchSeen := make(map[string]bool)
	for batchID := range state.batches {
		if !covers(recall, batchID) {
			continue
		}
		batchIDs = append(batchIDs, batchID)
		batchSeen[batchID] = true
	}

	for _, batchID := range batchIDs {
		batch := state.batches[batchID]
		patientReturnUsed := make(map[string]int)
		for _, record := range batch.dispenses {
			availableReturn := batch.returned[record.Patient] - patientReturnUsed[record.Patient]
			used := record.Qty
			if availableReturn < used {
				used = availableReturn
			}
			patientReturnUsed[record.Patient] += used
			if record.At >= recall.issueAt {
				outstanding := record.Qty - used
				if outstanding > 0 {
					if items[record.Patient] == nil {
						items[record.Patient] = make(map[string]int)
					}
					items[record.Patient][batchID] += outstanding
				}
			}
		}
	}

	patients := make([]string, 0, len(items))
	for patient := range items {
		patients = append(patients, patient)
	}
	sort.Strings(patients)
	result := make([]RecoveryItem, 0)
	for _, patient := range patients {
		batches := make([]string, 0)
		for batchID, qty := range items[patient] {
			if qty > 0 && batchSeen[batchID] {
				batches = append(batches, batchID)
			}
		}
		sort.Strings(batches)
		for _, batchID := range batches {
			result = append(result, RecoveryItem{
				Patient:     patient,
				Batch:       batchID,
				Outstanding: items[patient][batchID],
			})
		}
	}
	return result
}

func (s *System) RecoveryList(input RecoveryInput) ([]RecoveryItem, error) {
	if err := nonEmpty(input.ID, "召回编号"); err != nil {
		return nil, err
	}
	if err := validNow(input.Now); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return nil, err
	}
	recall := s.recalls[input.ID]
	if recall == nil {
		return nil, errorf(ErrNotFound, "召回 %s 不存在", input.ID)
	}
	if !recall.active {
		return nil, errorf(ErrInvalidState, "召回 %s 已解除", input.ID)
	}
	if recall.level == 3 {
		return nil, errorf(ErrInvalidState, "三级召回不提供追回清单")
	}
	s.clock = input.Now
	return recoveryItems(recall, s.drug(recall.drug)), nil
}

func (s *System) QueryBatch(input BatchQueryInput) (BatchView, error) {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return BatchView{}, err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return BatchView{}, err
	}
	if err := validNow(input.Now); err != nil {
		return BatchView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return BatchView{}, err
	}
	state, exists := s.drugs[input.Drug]
	var batch *batchState
	if exists {
		batch = state.batch(input.Batch)
	}
	if batch == nil {
		return BatchView{}, errorf(ErrNotFound, "药品 %s 批号 %s 不存在", input.Drug, input.Batch)
	}
	level, recallIDs := activeRecallLevel(state, input.Batch)
	positions := make([]string, 0, len(batch.stocks))
	for position := range batch.stocks {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i] == Warehouse {
			return true
		}
		if positions[j] == Warehouse {
			return false
		}
		return positions[i] < positions[j]
	})
	stocks := make([]PositionStock, 0, len(positions))
	for _, position := range positions {
		stocks = append(stocks, PositionStock{Position: position, Qty: batch.stocks[position]})
	}
	s.clock = input.Now
	return BatchView{
		Drug:      input.Drug,
		Batch:     input.Batch,
		Stocks:    stocks,
		Level:     level,
		RecallIDs: recallIDs,
	}, nil
}
