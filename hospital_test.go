package hospital

import (
	"errors"
	"reflect"
	"testing"
)

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("want %s, got %v", code, err)
	}
	if operationErr.Code != code {
		t.Fatalf("want %s, got %s (%s)", code, operationErr.Code, operationErr.Reason)
	}
}

func stockMap(view BatchView) map[string]int {
	result := make(map[string]int, len(view.Stocks))
	for _, stock := range view.Stocks {
		result[stock.Position] = stock.Qty
	}
	return result
}

func TestRecallAppliesToExistingAndFutureBatchesAndBounds(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 1, Drug: "d", Batch: "B100", Qty: 5}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 2, ID: "r", Drug: "d", First: "B100", Last: "B200", Level: 2, IssueAt: 1}))
	requireOK(t, system.Inbound(InboundInput{Now: 3, Drug: "d", Batch: "B200", Qty: 7}))
	requireOK(t, system.Inbound(InboundInput{Now: 4, Drug: "d", Batch: "B201", Qty: 9}))

	view100, err := system.QueryBatch(BatchQueryInput{Now: 4, Drug: "d", Batch: "B100"})
	requireOK(t, err)
	view200, err := system.QueryBatch(BatchQueryInput{Now: 4, Drug: "d", Batch: "B200"})
	requireOK(t, err)
	view201, err := system.QueryBatch(BatchQueryInput{Now: 4, Drug: "d", Batch: "B201"})
	requireOK(t, err)
	if view100.Level != 2 || view200.Level != 2 || view201.Level != 0 {
		t.Fatalf("closed interval levels = %d,%d,%d", view100.Level, view200.Level, view201.Level)
	}
	if !reflect.DeepEqual(view200.RecallIDs, []string{"r"}) {
		t.Fatalf("recall ids = %v", view200.RecallIDs)
	}
	if stockMap(view200)[Warehouse] != 7 {
		t.Fatalf("future inbound stock = %+v", view200.Stocks)
	}
}

func TestRecallLevelStackingAndCancel(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 1, Drug: "d", Batch: "B", Qty: 10}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 2, ID: "r3", Drug: "d", First: "B", Last: "B", Level: 3, IssueAt: 1}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 3, ID: "r2a", Drug: "d", First: "B", Last: "B", Level: 2, IssueAt: 1}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 4, ID: "r2b", Drug: "d", First: "B", Last: "B", Level: 2, IssueAt: 1}))

	view, err := system.QueryBatch(BatchQueryInput{Now: 4, Drug: "d", Batch: "B"})
	requireOK(t, err)
	if view.Level != 2 || !reflect.DeepEqual(view.RecallIDs, []string{"r2a", "r2b"}) {
		t.Fatalf("strictest level=%v ids=%v", view.Level, view.RecallIDs)
	}
	requireOK(t, system.CancelRecall(CancelRecallInput{Now: 5, ID: "r2a"}))
	view, err = system.QueryBatch(BatchQueryInput{Now: 5, Drug: "d", Batch: "B"})
	requireOK(t, err)
	if view.Level != 2 || !reflect.DeepEqual(view.RecallIDs, []string{"r2b"}) {
		t.Fatalf("after first cancel level=%v ids=%v", view.Level, view.RecallIDs)
	}
	requireOK(t, system.CancelRecall(CancelRecallInput{Now: 6, ID: "r2b"}))
	view, err = system.QueryBatch(BatchQueryInput{Now: 6, Drug: "d", Batch: "B"})
	requireOK(t, err)
	if view.Level != 3 || !reflect.DeepEqual(view.RecallIDs, []string{"r3"}) {
		t.Fatalf("fallback level=%v ids=%v", view.Level, view.RecallIDs)
	}
}

func TestTransferDispenseReturnMatrix(t *testing.T) {
	for _, level := range []int{1, 2, 3} {
		system := NewSystem()
		requireOK(t, system.Inbound(InboundInput{Now: 1, Drug: "d", Batch: "B", Qty: 100}))
		requireOK(t, system.Dispense(DispenseInput{Now: 2, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1}))
		recallID := ""
		if level > 0 {
			recallID = "r"
			requireOK(t, system.RegisterRecall(RecallInput{Now: 3, ID: recallID, Drug: "d", First: "B", Last: "B", Level: level, IssueAt: 1}))
		}

		errWard := system.Transfer(TransferInput{Now: 4, Drug: "d", Batch: "B", From: Warehouse, To: "ward", Qty: 1})
		if level == 0 || level == 3 {
			requireOK(t, errWard)
		} else {
			requireCode(t, errWard, ErrRecallBlocked)
		}

		errDispenseNoConsent := system.Dispense(DispenseInput{Now: 5, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1})
		errDispenseConsent := system.Dispense(DispenseInput{Now: 6, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1, InformedConsent: true})
		switch level {
		case 0:
			requireOK(t, errDispenseNoConsent)
		case 1, 2:
			requireCode(t, errDispenseNoConsent, ErrRecallBlocked)
			requireCode(t, errDispenseConsent, ErrRecallBlocked)
		case 3:
			requireCode(t, errDispenseNoConsent, ErrConsentRequired)
			requireOK(t, errDispenseConsent)
		}

		before, err := system.QueryBatch(BatchQueryInput{Now: 7, Drug: "d", Batch: "B"})
		requireOK(t, err)
		beforeWarehouse := stockMap(before)[Warehouse]
		requireOK(t, system.Return(ReturnInput{Now: 8, Drug: "d", Batch: "B", Patient: "p", Qty: 1}))
		after, err := system.QueryBatch(BatchQueryInput{Now: 8, Drug: "d", Batch: "B"})
		requireOK(t, err)
		if stockMap(after)[Warehouse] != beforeWarehouse+1 {
			t.Fatalf("level %d return did not enter warehouse: before=%d after=%v", level, beforeWarehouse, after.Stocks)
		}
	}
}

func TestLevelTwoAllowsOnlyWarehouseTransfer(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 1, Drug: "d", Batch: "B", Qty: 10}))
	requireOK(t, system.Transfer(TransferInput{Now: 2, Drug: "d", Batch: "B", From: Warehouse, To: "ward", Qty: 4}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 3, ID: "r", Drug: "d", First: "B", Last: "B", Level: 2, IssueAt: 1}))
	requireCode(t, system.Transfer(TransferInput{Now: 4, Drug: "d", Batch: "B", From: "ward", To: "ward2", Qty: 1}), ErrRecallBlocked)
	requireOK(t, system.Transfer(TransferInput{Now: 5, Drug: "d", Batch: "B", From: "ward", To: Warehouse, Qty: 3}))
	view, err := system.QueryBatch(BatchQueryInput{Now: 5, Drug: "d", Batch: "B"})
	requireOK(t, err)
	stocks := stockMap(view)
	if stocks[Warehouse] != 9 || stocks["ward"] != 1 {
		t.Fatalf("stocks = %v", stocks)
	}
}

func TestRecoveryFIFOAndZeroOmission(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 0, Drug: "d", Batch: "B", Qty: 100}))
	requireOK(t, system.Dispense(DispenseInput{Now: 1, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 3}))
	requireOK(t, system.Dispense(DispenseInput{Now: 2, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 4}))
	requireOK(t, system.Dispense(DispenseInput{Now: 2, Drug: "d", Batch: "B", Location: Warehouse, Patient: "q", Qty: 5}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 3, ID: "r", Drug: "d", First: "B", Last: "B", Level: 2, IssueAt: 2}))
	requireOK(t, system.Return(ReturnInput{Now: 4, Drug: "d", Batch: "B", Patient: "p", Qty: 3}))

	items, err := system.RecoveryList(RecoveryInput{Now: 4, ID: "r"})
	requireOK(t, err)
	want := []RecoveryItem{{Patient: "p", Batch: "B", Outstanding: 4}, {Patient: "q", Batch: "B", Outstanding: 5}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("items = %v, want %v", items, want)
	}
	requireOK(t, system.Return(ReturnInput{Now: 5, Drug: "d", Batch: "B", Patient: "q", Qty: 5}))
	items, err = system.RecoveryList(RecoveryInput{Now: 5, ID: "r"})
	requireOK(t, err)
	if !reflect.DeepEqual(items, []RecoveryItem{{Patient: "p", Batch: "B", Outstanding: 4}}) {
		t.Fatalf("zero item not omitted: %v", items)
	}
}

func TestRejectionsHaveNoSideEffectsAndPriority(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 10, Drug: "d", Batch: "B", Qty: 2}))
	view, err := system.QueryBatch(BatchQueryInput{Now: 10, Drug: "d", Batch: "B"})
	requireOK(t, err)

	requireCode(t, system.Dispense(DispenseInput{Now: 9, Drug: "", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1}), ErrInvalidArgument)
	requireCode(t, system.Dispense(DispenseInput{Now: 9, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1}), ErrClockRollback)
	requireCode(t, system.Dispense(DispenseInput{Now: 10, Drug: "x", Batch: "B", Location: Warehouse, Patient: "p", Qty: 1}), ErrNotFound)
	requireOK(t, system.RegisterRecall(RecallInput{Now: 10, ID: "r", Drug: "d", First: "B", Last: "B", Level: 1, IssueAt: 10}))
	requireCode(t, system.Dispense(DispenseInput{Now: 10, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 3, InformedConsent: true}), ErrRecallBlocked)
	after, err := system.QueryBatch(BatchQueryInput{Now: 10, Drug: "d", Batch: "B"})
	requireOK(t, err)
	if !reflect.DeepEqual(after.Stocks, view.Stocks) {
		t.Fatalf("stocks changed after rejection: before=%v after=%v", view.Stocks, after.Stocks)
	}
	_, err = system.RecoveryList(RecoveryInput{Now: 10, ID: "missing"})
	requireCode(t, err, ErrNotFound)
}

func TestRecoveryStateAndIssueAtBoundary(t *testing.T) {
	system := NewSystem()
	requireOK(t, system.Inbound(InboundInput{Now: 0, Drug: "d", Batch: "B", Qty: 10}))
	requireOK(t, system.Dispense(DispenseInput{Now: 5, Drug: "d", Batch: "B", Location: Warehouse, Patient: "p", Qty: 2}))
	requireOK(t, system.RegisterRecall(RecallInput{Now: 6, ID: "r3", Drug: "d", First: "B", Last: "B", Level: 3, IssueAt: 5}))
	_, err := system.RecoveryList(RecoveryInput{Now: 6, ID: "r3"})
	requireCode(t, err, ErrInvalidState)
	requireOK(t, system.CancelRecall(CancelRecallInput{Now: 7, ID: "r3"}))
	_, err = system.RecoveryList(RecoveryInput{Now: 7, ID: "r3"})
	requireCode(t, err, ErrInvalidState)

	requireOK(t, system.RegisterRecall(RecallInput{Now: 8, ID: "r2", Drug: "d", First: "B", Last: "B", Level: 2, IssueAt: 5}))
	items, err := system.RecoveryList(RecoveryInput{Now: 8, ID: "r2"})
	requireOK(t, err)
	if !reflect.DeepEqual(items, []RecoveryItem{{Patient: "p", Batch: "B", Outstanding: 2}}) {
		t.Fatalf("issueAt boundary items = %v", items)
	}
}
