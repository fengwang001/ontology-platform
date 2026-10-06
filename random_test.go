package hospital

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type randomOperation struct {
	name  string
	input any
	why   string
}

type randomScenario struct {
	drugs     []string
	batches   map[string][]string
	patients  []string
	positions []string
	recallIDs map[string]int
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr.Code
	}
	return ErrorCode(err.Error())
}

func assertSystemInvariant(t *testing.T, system *System) {
	t.Helper()
	for drugID, state := range system.drugs {
		for batchID, batch := range state.batches {
			stockTotal := 0
			for _, qty := range batch.stocks {
				stockTotal += qty
			}
			heldTotal := 0
			for patient, dispensed := range batch.dispensed {
				heldTotal += dispensed - batch.returned[patient]
			}
			if stockTotal+heldTotal != batch.total {
				t.Fatalf("invariant broken %s/%s: stock=%d held=%d total=%d", drugID, batchID, stockTotal, heldTotal, batch.total)
			}
		}
	}
}

func assertNaiveInvariant(t *testing.T, model *naiveSystem) {
	t.Helper()
	for drugID, state := range model.drugs {
		for batchID, batch := range state.batches {
			stockTotal := 0
			for _, qty := range batch.stocks {
				stockTotal += qty
			}
			heldTotal := 0
			for patient, dispensed := range batch.dispensed {
				heldTotal += dispensed - batch.returned[patient]
			}
			if stockTotal+heldTotal != batch.total {
				t.Fatalf("naive invariant broken %s/%s: stock=%d held=%d total=%d", drugID, batchID, stockTotal, heldTotal, batch.total)
			}
		}
	}
}

func generateRandomOperations(t *testing.T, source *rand.Rand) []randomOperation {
	t.Helper()
	scenario := randomScenario{
		drugs:     []string{"阿司匹林", "布洛芬", "青霉素"},
		batches:   make(map[string][]string),
		patients:  []string{"p1", "p2", "p3"},
		positions: []string{Warehouse, "ward-a", "ward-b"},
		recallIDs: make(map[string]int),
	}
	operations := make([]randomOperation, 0, source.Intn(25)+25)
	now := int64(0)
	knownBatch := func() (string, string, bool) {
		drug := scenario.drugs[source.Intn(len(scenario.drugs))]
		if len(scenario.batches[drug]) == 0 {
			return "", "", false
		}
		return drug, scenario.batches[drug][source.Intn(len(scenario.batches[drug]))], true
	}
	choosePosition := func() string { return scenario.positions[source.Intn(len(scenario.positions))] }

	for step := 0; step < cap(operations); step++ {
		if source.Intn(10) == 0 && now > 0 {
			now--
		} else {
			now += int64(source.Intn(5))
		}
		if now < 0 {
			now = 0
		}
		if now > 1_000_000_000 {
			now = 1_000_000_000
		}

		switch source.Intn(10) {
		case 0:
			drug := scenario.drugs[source.Intn(len(scenario.drugs))]
			batch := fmt.Sprintf("B%03d", len(scenario.batches[drug])+source.Intn(3))
			qty := source.Intn(8) + 1
			if source.Intn(20) == 0 {
				qty = 0
			}
			operations = append(operations, randomOperation{
				name:  "inbound",
				why:   fmt.Sprintf("登记 %s/%s，重复批号由系统判定", drug, batch),
				input: InboundInput{Now: now, Drug: drug, Batch: batch, Qty: qty},
			})
			scenario.batches[drug] = append(scenario.batches[drug], batch)
		case 1:
			if drug, batch, ok := knownBatch(); ok {
				operations = append(operations, randomOperation{
					name:  "transfer",
					why:   "按当前有效等级和出发位置库存判定调拨",
					input: TransferInput{Now: now, Drug: drug, Batch: batch, From: choosePosition(), To: choosePosition(), Qty: source.Intn(6) + 1},
				})
			}
		case 2:
			if drug, batch, ok := knownBatch(); ok {
				operations = append(operations, randomOperation{
					name:  "dispense",
					why:   "一级/二级禁止、三级查知情确认、再查库存",
					input: DispenseInput{Now: now, Drug: drug, Batch: batch, Location: choosePosition(), Patient: scenario.patients[source.Intn(len(scenario.patients))], Qty: source.Intn(6) + 1, InformedConsent: source.Intn(2) == 0},
				})
			}
		case 3:
			if drug, batch, ok := knownBatch(); ok {
				operations = append(operations, randomOperation{
					name:  "return",
					why:   "退药按患者持有余量判定，成功后进入药库",
					input: ReturnInput{Now: now, Drug: drug, Batch: batch, Patient: scenario.patients[source.Intn(len(scenario.patients))], Qty: source.Intn(5) + 1},
				})
			}
		case 4:
			id := fmt.Sprintf("r%03d", len(scenario.recallIDs)+source.Intn(4))
			first := fmt.Sprintf("B%03d", source.Intn(7))
			last := fmt.Sprintf("B%03d", source.Intn(7))
			if first > last {
				first, last = last, first
			}
			issueAt := now - int64(source.Intn(4))
			if issueAt < 0 {
				issueAt = 0
			}
			operations = append(operations, randomOperation{
				name:  "register-recall",
				why:   "区间覆盖现有与未来批次，等级叠加后取最严",
				input: RecallInput{Now: now, ID: id, Drug: scenario.drugs[source.Intn(len(scenario.drugs))], First: first, Last: last, Level: source.Intn(3) + 1, IssueAt: issueAt},
			})
			scenario.recallIDs[id]++
		case 5:
			if len(scenario.recallIDs) > 0 {
				ids := make([]string, 0, len(scenario.recallIDs))
				for id := range scenario.recallIDs {
					ids = append(ids, id)
				}
				id := ids[source.Intn(len(ids))]
				operations = append(operations, randomOperation{
					name:  "cancel-recall",
					why:   "只撤销该条召回，重复解除返回状态不符",
					input: CancelRecallInput{Now: now, ID: id},
				})
			}
		case 6:
			if len(scenario.recallIDs) > 0 {
				ids := make([]string, 0, len(scenario.recallIDs))
				for id := range scenario.recallIDs {
					ids = append(ids, id)
				}
				operations = append(operations, randomOperation{
					name:  "recovery",
					why:   "按 FIFO 退药抵扣后汇总始发时刻以来未退回数量",
					input: RecoveryInput{Now: now, ID: ids[source.Intn(len(ids))]},
				})
			}
		default:
			if drug, batch, ok := knownBatch(); ok {
				operations = append(operations, randomOperation{
					name:  "query",
					why:   "扫描该药品活跃召回并输出最严编号集合",
					input: BatchQueryInput{Now: now, Drug: drug, Batch: batch},
				})
			}
		}
	}
	return operations
}

func applyToBoth(t *testing.T, system *System, model *naiveSystem, operation randomOperation) (ErrorCode, string, error) {
	t.Helper()
	var systemErr error
	var modelErr error
	var systemItems []RecoveryItem
	var modelItems []RecoveryItem
	var systemView BatchView
	var modelView BatchView

	switch input := operation.input.(type) {
	case InboundInput:
		systemErr = system.Inbound(input)
		modelErr = model.inbound(input)
	case TransferInput:
		systemErr = system.Transfer(input)
		modelErr = model.transfer(input)
	case DispenseInput:
		systemErr = system.Dispense(input)
		modelErr = model.dispense(input)
	case ReturnInput:
		systemErr = system.Return(input)
		modelErr = model.returnMedication(input)
	case RecallInput:
		systemErr = system.RegisterRecall(input)
		modelErr = model.registerRecall(input)
	case CancelRecallInput:
		systemErr = system.CancelRecall(input)
		modelErr = model.cancelRecall(input)
	case RecoveryInput:
		systemItems, systemErr = system.RecoveryList(input)
		modelItems, modelErr = model.recoveryList(input)
	case BatchQueryInput:
		systemView, systemErr = system.QueryBatch(input)
		modelView, modelErr = model.queryBatch(input)
	default:
		t.Fatalf("unknown operation %T", operation.input)
	}

	systemCode := codeOf(systemErr)
	modelCode := codeOf(modelErr)
	if systemCode != modelCode {
		return "", "", fmt.Errorf("error code mismatch: actual=%q naive=%q", systemCode, modelCode)
	}
	if !reflect.DeepEqual(systemItems, modelItems) {
		return "", "", fmt.Errorf("recovery mismatch: actual=%v naive=%v", systemItems, modelItems)
	}
	if !reflect.DeepEqual(systemView, modelView) {
		return "", "", fmt.Errorf("batch view mismatch: actual=%+v naive=%+v", systemView, modelView)
	}
	output := "accepted"
	if operation.name == "recovery" {
		output = fmt.Sprintf("items=%v", systemItems)
	}
	if operation.name == "query" {
		output = fmt.Sprintf("view=%+v", systemView)
	}
	return systemCode, output, nil
}

func TestRandomEquivalence(t *testing.T) {
	for scenarioID := 0; scenarioID < 1500; scenarioID++ {
		source := rand.New(rand.NewSource(int64(scenarioID) + 1))
		operations := generateRandomOperations(t, source)
		system := NewSystem()
		model := newNaiveSystem()
		t.Logf("scenario=%d steps=%d", scenarioID, len(operations))
		for step, operation := range operations {
			code, output, err := applyToBoth(t, system, model, operation)
			t.Logf("step=%d op=%s input=%+v reason=%s output=%s error_code=%s", step, operation.name, operation.input, operation.why, output, code)
			if err != nil {
				t.Fatalf("scenario %d step %d: %v", scenarioID, step, err)
			}
			assertSystemInvariant(t, system)
			assertNaiveInvariant(t, model)
		}
	}
}
