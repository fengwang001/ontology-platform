package hospital

import (
	"sort"
)

func covers(recall *recallRecord, batch string) bool {
	return recall.first <= batch && batch <= recall.last
}

func activeRecallLevel(state *drugState, batch string) (int, []string) {
	level := 0
	var ids []string
	for id, recall := range state.activeRecalls {
		if !covers(recall, batch) {
			continue
		}
		if level == 0 || recall.level < level {
			level = recall.level
			ids = append(ids[:0], id)
		} else if recall.level == level {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return level, ids
}

func (s *System) Inbound(input InboundInput) error {
	if err := s.validateCommon(input.Now, input.Drug, input.Batch, input.Qty); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	state, exists := s.drugs[input.Drug]
	if exists && state.batch(input.Batch) != nil {
		return errorf(ErrInvalidState, "药品 %s 批号 %s 已入库", input.Drug, input.Batch)
	}
	if !exists {
		state = s.drug(input.Drug)
	}
	state.batches[input.Batch] = &batchState{
		drug:      input.Drug,
		id:        input.Batch,
		total:     input.Qty,
		stocks:    map[string]int{Warehouse: input.Qty},
		returned:  make(map[string]int),
		dispensed: make(map[string]int),
	}
	s.clock = input.Now
	return nil
}

func (s *System) Transfer(input TransferInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := validPosition(input.From, "出发位置"); err != nil {
		return err
	}
	if err := validPosition(input.To, "目标位置"); err != nil {
		return err
	}
	if input.From == input.To {
		return invalidf("调拨的出发位置和目标位置不能相同")
	}
	if err := validQty(input.Qty); err != nil {
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
	state, exists := s.drugs[input.Drug]
	if !exists || state.batch(input.Batch) == nil {
		return errorf(ErrNotFound, "药品 %s 批号 %s 不存在", input.Drug, input.Batch)
	}
	batch := state.batch(input.Batch)
	level, _ := activeRecallLevel(state, input.Batch)
	if level == 1 {
		return errorf(ErrRecallBlocked, "一级召回冻结库存，禁止调拨")
	}
	if level == 2 && input.To != Warehouse {
		return errorf(ErrRecallBlocked, "二级召回只允许调入药库")
	}
	if batch.stocks[input.From] < input.Qty {
		return errorf(ErrInsufficient, "位置 %s 库存不足", input.From)
	}

	batch.stocks[input.From] -= input.Qty
	batch.stocks[input.To] += input.Qty
	s.clock = input.Now
	return nil
}

func (s *System) Dispense(input DispenseInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := validPosition(input.Location, "发放位置"); err != nil {
		return err
	}
	if err := nonEmpty(input.Patient, "患者"); err != nil {
		return err
	}
	if err := validQty(input.Qty); err != nil {
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
	state, exists := s.drugs[input.Drug]
	if !exists || state.batch(input.Batch) == nil {
		return errorf(ErrNotFound, "药品 %s 批号 %s 不存在", input.Drug, input.Batch)
	}
	batch := state.batch(input.Batch)
	level, _ := activeRecallLevel(state, input.Batch)
	if level == 1 || level == 2 {
		return errorf(ErrRecallBlocked, "%d 级召回禁止发放", level)
	}
	if level == 3 && !input.InformedConsent {
		return errorf(ErrConsentRequired, "三级召回发放需要知情确认")
	}
	if batch.stocks[input.Location] < input.Qty {
		return errorf(ErrInsufficient, "位置 %s 库存不足", input.Location)
	}

	batch.dispenseSeq++
	batch.dispenses = append(batch.dispenses, dispenseRecord{
		Seq:      batch.dispenseSeq,
		At:       input.Now,
		Patient:  input.Patient,
		Location: input.Location,
		Qty:      input.Qty,
	})
	batch.stocks[input.Location] -= input.Qty
	batch.dispensed[input.Patient] += input.Qty
	s.clock = input.Now
	return nil
}

func (s *System) Return(input ReturnInput) error {
	if err := s.validateCommon(input.Now, input.Drug, input.Batch, input.Qty); err != nil {
		return err
	}
	if err := nonEmpty(input.Patient, "患者"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	state, exists := s.drugs[input.Drug]
	if !exists || state.batch(input.Batch) == nil {
		return errorf(ErrNotFound, "药品 %s 批号 %s 不存在", input.Drug, input.Batch)
	}
	batch := state.batch(input.Batch)
	outstanding := batch.dispensed[input.Patient] - batch.returned[input.Patient]
	if input.Qty > outstanding {
		return errorf(ErrReturnExceeded, "退药数量超过患者未退回数量")
	}

	batch.returned[input.Patient] += input.Qty
	batch.stocks[Warehouse] += input.Qty
	s.clock = input.Now
	return nil
}
