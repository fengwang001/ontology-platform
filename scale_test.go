package hospital

import (
	"fmt"
	"sync"
	"testing"
)

func TestScaleSkeleton(t *testing.T) {
	_ = NewSystem()
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	system := NewSystem()
	const workers = 40
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers*2)

	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			errs <- system.Inbound(InboundInput{
				Now:   10,
				Drug:  "d",
				Batch: fmt.Sprintf("B%03d", worker),
				Qty:   2,
			})
		}(worker)
	}
	waitGroup.Wait()
	waitGroup.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer waitGroup.Done()
			_, err := system.QueryBatch(BatchQueryInput{Now: 10, Drug: "d", Batch: fmt.Sprintf("B%03d", worker)})
			errs <- err
		}(worker)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent operation failed: %v", err)
		}
	}

	for worker := 0; worker < workers; worker++ {
		view, err := system.QueryBatch(BatchQueryInput{Now: 10, Drug: "d", Batch: fmt.Sprintf("B%03d", worker)})
		if err != nil {
			t.Fatalf("missing concurrent batch: %v", err)
		}
		if stockMap(view)[Warehouse] != 2 {
			t.Fatalf("batch %d stock = %v", worker, view.Stocks)
		}
	}
}

func buildLevelScaleCase(tb testing.TB, scale int) (*System, *naiveSystem) {
	tb.Helper()
	system := NewSystem()
	model := newNaiveSystem()
	if err := system.Inbound(InboundInput{Now: 1, Drug: "target", Batch: "B000", Qty: 1}); err != nil {
		tb.Fatal(err)
	}
	if err := model.inbound(InboundInput{Now: 1, Drug: "target", Batch: "B000", Qty: 1}); err != nil {
		tb.Fatal(err)
	}
	for index := 0; index < scale; index++ {
		at := int64(2 + index*3)
		id := fmt.Sprintf("canceled-%04d", index)
		input := RecallInput{Now: at, ID: id, Drug: "target", First: "A", Last: "Z", Level: 1, IssueAt: 1}
		if err := system.RegisterRecall(input); err != nil {
			tb.Fatal(err)
		}
		if err := model.registerRecall(input); err != nil {
			tb.Fatal(err)
		}
		if err := system.CancelRecall(CancelRecallInput{Now: at + 1, ID: id}); err != nil {
			tb.Fatal(err)
		}
		if err := model.cancelRecall(CancelRecallInput{Now: at + 1, ID: id}); err != nil {
			tb.Fatal(err)
		}

		otherDrug := fmt.Sprintf("other-%04d", index)
		otherInput := RecallInput{Now: at + 2, ID: fmt.Sprintf("other-%04d", index), Drug: otherDrug, First: "A", Last: "Z", Level: 1, IssueAt: 1}
		if err := system.RegisterRecall(otherInput); err != nil {
			tb.Fatal(err)
		}
		if err := model.registerRecall(otherInput); err != nil {
			tb.Fatal(err)
		}
	}
	activeAt := int64(2 + scale*3)
	if err := system.RegisterRecall(RecallInput{Now: activeAt, ID: "active", Drug: "target", First: "B000", Last: "B000", Level: 3, IssueAt: 1}); err != nil {
		tb.Fatal(err)
	}
	if err := model.registerRecall(RecallInput{Now: activeAt, ID: "active", Drug: "target", First: "B000", Last: "B000", Level: 3, IssueAt: 1}); err != nil {
		tb.Fatal(err)
	}
	return system, model
}

func BenchmarkLevelQueryTwoScales(b *testing.B) {
	for _, scale := range []int{1000, 4000} {
		system, model := buildLevelScaleCase(b, scale)
		query := BatchQueryInput{Now: int64(2 + scale*3), Drug: "target", Batch: "B000"}
		b.Run(fmt.Sprintf("indexed_%d", scale), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := system.QueryBatch(query); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("naive_%d", scale), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := model.queryBatch(query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func buildRecoveryScaleCase(tb testing.TB, scale int) (*System, *naiveSystem) {
	tb.Helper()
	system := NewSystem()
	model := newNaiveSystem()
	if err := system.Inbound(InboundInput{Now: 1, Drug: "target", Batch: "B000", Qty: 1_000_000}); err != nil {
		tb.Fatal(err)
	}
	if err := model.inbound(InboundInput{Now: 1, Drug: "target", Batch: "B000", Qty: 1_000_000}); err != nil {
		tb.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		input := DispenseInput{
			Now:      int64(index + 3),
			Drug:     "target",
			Batch:    "B000",
			Location: Warehouse,
			Patient:  fmt.Sprintf("p%02d", index%5),
			Qty:      1,
		}
		if err := system.Dispense(input); err != nil {
			tb.Fatal(err)
		}
		if err := model.dispense(input); err != nil {
			tb.Fatal(err)
		}
	}
	if err := system.RegisterRecall(RecallInput{Now: 103, ID: "target", Drug: "target", First: "B000", Last: "B000", Level: 2, IssueAt: 2}); err != nil {
		tb.Fatal(err)
	}
	if err := model.registerRecall(RecallInput{Now: 103, ID: "target", Drug: "target", First: "B000", Last: "B000", Level: 2, IssueAt: 2}); err != nil {
		tb.Fatal(err)
	}

	for index := 0; index < scale; index++ {
		drugID := fmt.Sprintf("other-%04d", index)
		batchID := "X000"
		systemBatch := &batchState{
			drug: drugID, id: batchID, total: 1_000_000,
			stocks:    map[string]int{},
			returned:  map[string]int{},
			dispensed: map[string]int{},
		}
		modelBatch := &naiveBatch{
			total:     1_000_000,
			stocks:    map[string]int{},
			returned:  map[string]int{},
			dispensed: map[string]int{},
		}
		for record := 0; record < 50; record++ {
			dispense := dispenseRecord{Seq: record + 1, At: int64(record + 3), Patient: "other-patient", Location: Warehouse, Qty: 1}
			systemBatch.dispenses = append(systemBatch.dispenses, dispense)
			modelBatch.dispenses = append(modelBatch.dispenses, dispense)
		}
		system.drug(drugID).batches[batchID] = systemBatch
		model.drug(drugID).batches[batchID] = modelBatch
	}
	return system, model
}

func BenchmarkRecoveryListTwoScales(b *testing.B) {
	for _, scale := range []int{1000, 4000} {
		system, model := buildRecoveryScaleCase(b, scale)
		input := RecoveryInput{Now: 103, ID: "target"}
		b.Run(fmt.Sprintf("scoped_%d", scale), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := system.RecoveryList(input); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("global_other_drug_scan_%d", scale), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := model.globalRecoveryList(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
