package hospital

import "sort"

type naiveBatch struct {
	total     int
	stocks    map[string]int
	dispenses []dispenseRecord
	returned  map[string]int
	dispensed map[string]int
	seq       int
}

type naiveDrug struct {
	batches map[string]*naiveBatch
}

type naiveRecall struct {
	id      string
	drug    string
	first   string
	last    string
	level   int
	issueAt int64
	active  bool
}

type naiveSystem struct {
	clock   int64
	drugs   map[string]*naiveDrug
	recalls []*naiveRecall
}

func newNaiveSystem() *naiveSystem {
	return &naiveSystem{drugs: make(map[string]*naiveDrug)}
}

func (model *naiveSystem) drug(id string) *naiveDrug {
	state := model.drugs[id]
	if state == nil {
		state = &naiveDrug{batches: make(map[string]*naiveBatch)}
		model.drugs[id] = state
	}
	return state
}

func (model *naiveSystem) validate(now int64) error {
	if err := validNow(now); err != nil {
		return err
	}
	if now < model.clock {
		return errorf(ErrClockRollback, "naive clock rollback")
	}
	return nil
}

func (model *naiveSystem) batch(drug, batch string) (*naiveDrug, *naiveBatch, bool) {
	state, exists := model.drugs[drug]
	if !exists {
		return nil, nil, false
	}
	item := state.batches[batch]
	return state, item, item != nil
}

func (model *naiveSystem) level(drugID, batch string) (int, []string) {
	level := 0
	var ids []string
	for _, recall := range model.recalls {
		if recall.active && recall.drug == drugID && recall.first <= batch && batch <= recall.last {
			if level == 0 || recall.level < level {
				level = recall.level
				ids = append(ids[:0], recall.id)
			} else if recall.level == level {
				ids = append(ids, recall.id)
			}
		}
	}
	sort.Strings(ids)
	return level, ids
}

func (model *naiveSystem) inbound(input InboundInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := validQty(input.Qty); err != nil {
		return err
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	state := model.drug(input.Drug)
	if state.batches[input.Batch] != nil {
		return errorf(ErrInvalidState, "duplicate batch")
	}
	state.batches[input.Batch] = &naiveBatch{
		total:     input.Qty,
		stocks:    map[string]int{Warehouse: input.Qty},
		returned:  make(map[string]int),
		dispensed: make(map[string]int),
	}
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) transfer(input TransferInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := nonEmpty(input.From, "出发位置"); err != nil {
		return err
	}
	if err := nonEmpty(input.To, "目标位置"); err != nil {
		return err
	}
	if input.From == input.To {
		return invalidf("same position")
	}
	if err := validQty(input.Qty); err != nil {
		return err
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	_, batch, exists := model.batch(input.Drug, input.Batch)
	if !exists {
		return errorf(ErrNotFound, "missing batch")
	}
	level, _ := model.level(input.Drug, input.Batch)
	if level == 1 {
		return errorf(ErrRecallBlocked, "level 1 transfer")
	}
	if level == 2 && input.To != Warehouse {
		return errorf(ErrRecallBlocked, "level 2 transfer")
	}
	if batch.stocks[input.From] < input.Qty {
		return errorf(ErrInsufficient, "stock")
	}
	batch.stocks[input.From] -= input.Qty
	batch.stocks[input.To] += input.Qty
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) dispense(input DispenseInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := nonEmpty(input.Location, "发放位置"); err != nil {
		return err
	}
	if err := nonEmpty(input.Patient, "患者"); err != nil {
		return err
	}
	if err := validQty(input.Qty); err != nil {
		return err
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	_, batch, exists := model.batch(input.Drug, input.Batch)
	if !exists {
		return errorf(ErrNotFound, "missing batch")
	}
	level, _ := model.level(input.Drug, input.Batch)
	if level == 1 || level == 2 {
		return errorf(ErrRecallBlocked, "recall dispense")
	}
	if level == 3 && !input.InformedConsent {
		return errorf(ErrConsentRequired, "consent")
	}
	if batch.stocks[input.Location] < input.Qty {
		return errorf(ErrInsufficient, "stock")
	}
	batch.seq++
	batch.dispenses = append(batch.dispenses, dispenseRecord{
		Seq:      batch.seq,
		At:       input.Now,
		Patient:  input.Patient,
		Location: input.Location,
		Qty:      input.Qty,
	})
	batch.stocks[input.Location] -= input.Qty
	batch.dispensed[input.Patient] += input.Qty
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) returnMedication(input ReturnInput) error {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return err
	}
	if err := validQty(input.Qty); err != nil {
		return err
	}
	if err := nonEmpty(input.Patient, "患者"); err != nil {
		return err
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	_, batch, exists := model.batch(input.Drug, input.Batch)
	if !exists {
		return errorf(ErrNotFound, "missing batch")
	}
	if input.Qty > batch.dispensed[input.Patient]-batch.returned[input.Patient] {
		return errorf(ErrReturnExceeded, "return")
	}
	batch.returned[input.Patient] += input.Qty
	batch.stocks[Warehouse] += input.Qty
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) registerRecall(input RecallInput) error {
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
	if input.First > input.Last || input.Level < 1 || input.Level > 3 || input.IssueAt < 0 || input.IssueAt > 1_000_000_000 || input.IssueAt > input.Now {
		return invalidf("bad recall")
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	for _, recall := range model.recalls {
		if recall.id == input.ID {
			return errorf(ErrInvalidState, "duplicate recall")
		}
	}
	model.recalls = append(model.recalls, &naiveRecall{
		id:      input.ID,
		drug:    input.Drug,
		first:   input.First,
		last:    input.Last,
		level:   input.Level,
		issueAt: input.IssueAt,
		active:  true,
	})
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) cancelRecall(input CancelRecallInput) error {
	if err := nonEmpty(input.ID, "召回编号"); err != nil {
		return err
	}
	if err := model.validate(input.Now); err != nil {
		return err
	}
	var target *naiveRecall
	for _, recall := range model.recalls {
		if recall.id == input.ID {
			target = recall
			break
		}
	}
	if target == nil {
		return errorf(ErrNotFound, "missing recall")
	}
	if !target.active {
		return errorf(ErrInvalidState, "canceled")
	}
	target.active = false
	model.clock = input.Now
	return nil
}

func (model *naiveSystem) recoveryList(input RecoveryInput) ([]RecoveryItem, error) {
	if err := nonEmpty(input.ID, "召回编号"); err != nil {
		return nil, err
	}
	if err := model.validate(input.Now); err != nil {
		return nil, err
	}
	var target *naiveRecall
	for _, recall := range model.recalls {
		if recall.id == input.ID {
			target = recall
			break
		}
	}
	if target == nil {
		return nil, errorf(ErrNotFound, "missing recall")
	}
	if !target.active {
		return nil, errorf(ErrInvalidState, "canceled")
	}
	if target.level == 3 {
		return nil, errorf(ErrInvalidState, "level 3")
	}
	items := make(map[string]map[string]int)
	state := model.drug(target.drug)
	batchIDs := make([]string, 0)
	for batchID := range state.batches {
		if target.first <= batchID && batchID <= target.last {
			batchIDs = append(batchIDs, batchID)
		}
	}
	sort.Strings(batchIDs)
	for _, batchID := range batchIDs {
		batch := state.batches[batchID]
		usedReturns := make(map[string]int)
		for _, record := range batch.dispenses {
			available := batch.returned[record.Patient] - usedReturns[record.Patient]
			used := record.Qty
			if available < used {
				used = available
			}
			usedReturns[record.Patient] += used
			if record.At >= target.issueAt && record.Qty-used > 0 {
				if items[record.Patient] == nil {
					items[record.Patient] = make(map[string]int)
				}
				items[record.Patient][batchID] += record.Qty - used
			}
		}
	}
	patients := make([]string, 0, len(items))
	for patient := range items {
		patients = append(patients, patient)
	}
	sort.Strings(patients)
	result := []RecoveryItem{}
	for _, patient := range patients {
		batches := make([]string, 0)
		for batchID, qty := range items[patient] {
			if qty > 0 {
				batches = append(batches, batchID)
			}
		}
		sort.Strings(batches)
		for _, batchID := range batches {
			result = append(result, RecoveryItem{Patient: patient, Batch: batchID, Outstanding: items[patient][batchID]})
		}
	}
	model.clock = input.Now
	return result, nil
}

func (model *naiveSystem) queryBatch(input BatchQueryInput) (BatchView, error) {
	if err := nonEmpty(input.Drug, "药品"); err != nil {
		return BatchView{}, err
	}
	if err := nonEmpty(input.Batch, "批号"); err != nil {
		return BatchView{}, err
	}
	if err := model.validate(input.Now); err != nil {
		return BatchView{}, err
	}
	_, batch, exists := model.batch(input.Drug, input.Batch)
	if !exists {
		return BatchView{}, errorf(ErrNotFound, "missing batch")
	}
	level, ids := model.level(input.Drug, input.Batch)
	positions := make([]string, 0)
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
	model.clock = input.Now
	return BatchView{Drug: input.Drug, Batch: input.Batch, Stocks: stocks, Level: level, RecallIDs: ids}, nil
}

func (model *naiveSystem) globalRecoveryList(input RecoveryInput) ([]RecoveryItem, error) {
	var target *naiveRecall
	for _, recall := range model.recalls {
		if recall.id == input.ID {
			target = recall
			break
		}
	}
	if target == nil || !target.active || target.level == 3 {
		return nil, errorf(ErrInvalidState, "global recovery state")
	}
	items := make(map[string]map[string]int)
	for drugID, state := range model.drugs {
		for batchID, batch := range state.batches {
			if drugID != target.drug || batchID < target.first || batchID > target.last {
				continue
			}
			usedReturns := make(map[string]int)
			for _, record := range batch.dispenses {
				available := batch.returned[record.Patient] - usedReturns[record.Patient]
				used := record.Qty
				if available < used {
					used = available
				}
				usedReturns[record.Patient] += used
				if record.At >= target.issueAt && record.Qty-used > 0 {
					if items[record.Patient] == nil {
						items[record.Patient] = make(map[string]int)
					}
					items[record.Patient][batchID] += record.Qty - used
				}
			}
		}
	}
	patients := make([]string, 0, len(items))
	for patient := range items {
		patients = append(patients, patient)
	}
	sort.Strings(patients)
	result := []RecoveryItem{}
	for _, patient := range patients {
		batches := make([]string, 0)
		for batchID, qty := range items[patient] {
			if qty > 0 {
				batches = append(batches, batchID)
			}
		}
		sort.Strings(batches)
		for _, batchID := range batches {
			result = append(result, RecoveryItem{Patient: patient, Batch: batchID, Outstanding: items[patient][batchID]})
		}
	}
	return result, nil
}
