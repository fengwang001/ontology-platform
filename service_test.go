package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func testService() *Service {
	return NewService([]Room{{ID: 1, Area: 1}, {ID: 2, Area: 3}}, 0, 2)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func code(err error) ErrorCode {
	if err == nil {
		return ""
	}
	return err.(*ServiceError).Code
}

func TestMoveInAndMoveOutAreHalfOpen(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 10, Start: 0, End: 5, PayerID: 1}))
	got := sumShares(service.allocations[1])
	if got != 10 {
		t.Fatalf("allocation total = %d, want 10", got)
	}
	if service.allocations[1][0].TenantID != 1 || service.allocations[1][0].Amount != 2 {
		t.Fatalf("move-in day share = %+v", service.allocations[1][0])
	}
	must(t, service.MoveOut(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 4}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 1, Day: 4}))
	service.rebuild()
	counts := make(map[int]int)
	var landlord int
	for _, share := range service.allocations[1] {
		if share.Landlord {
			landlord += share.Amount
		} else {
			counts[share.TenantID] += share.Amount
		}
	}
	if counts[1] != 8 || counts[2] != 2 || landlord != 0 {
		t.Fatalf("half-open handoff shares = tenant1:%d tenant2:%d landlord:%d", counts[1], counts[2], landlord)
	}
}

func TestVacantDayFallsBackToEarliestFutureThenLandlord(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 5}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 3}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 10, Start: 0, End: 5, Landlord: true}))
	got := sumShares(service.allocations[1])
	if got != 10 {
		t.Fatalf("allocation total = %d", got)
	}
	if service.allocations[1][0].TenantID != 1 {
		t.Fatalf("vacant day assigned to %+v, want tenant 1", service.allocations[1][0])
	}

	service = testService()
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 4, Start: 0, End: 2, Landlord: true}))
	for _, share := range service.allocations[1] {
		if !share.Landlord {
			t.Fatalf("share with no future tenant = %+v, want landlord", share)
		}
	}
}

func TestRemainderOrderAndAreaMoveDay(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 1}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 1}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 5, Start: 1, End: 2, PayerID: 2}))
	if amountFor(service.allocations[1], 1) != 3 || amountFor(service.allocations[1], 2) != 2 {
		t.Fatalf("headcount remainder = %+v", service.allocations[1])
	}

	service = testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.MoveRoom(MoveInput{Now: 0, TenantID: 1, RoomID: 2, Day: 1}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 1, Day: 1}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 8, Start: 0, End: 2, PayerID: 2, ByArea: true}))
	if amountFor(service.allocations[1], 1) != 7 || amountFor(service.allocations[1], 2) != 1 {
		t.Fatalf("move-day area allocation = %+v", service.allocations[1])
	}
}

func TestNetReversalKeepsNetAndConservation(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 0}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 10, Start: 0, End: 1, PayerID: 1}))
	net, err := service.NetBetween(1, 2)
	must(t, err)
	if net != 5 {
		t.Fatalf("net = %d, want 5", net)
	}
	must(t, service.AddBill(BillInput{Now: 0, BillID: 2, Amount: 20, Start: 0, End: 1, PayerID: 2}))
	net, err = service.NetBetween(1, 2)
	must(t, err)
	if net != -5 {
		t.Fatalf("reversed net = %d, want -5", net)
	}
	if sumTenantBalances(service.net, 1, 2) != 0 {
		t.Fatalf("tenant balance sum = %d", sumTenantBalances(service.net, 1, 2))
	}
}

func TestSettlementSupplementDisputeAndAdjudication(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 0}))
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 10, Start: 0, End: 2, PayerID: 1}))
	must(t, service.MoveOut(MoveInput{Now: 1, TenantID: 1, RoomID: 1, Day: 1}))
	if len(service.settlements[1]) != 1 {
		t.Fatalf("initial settlement = %+v", service.settlements[1])
	}
	must(t, service.AddBill(BillInput{Now: 1, BillID: 2, Amount: 8, Start: 0, End: 2, PayerID: 2}))
	if len(service.settlements[1]) != 2 || !service.settlements[1][1].Supplement || settlementTotal(service.settlements[1][1]) != -2 {
		t.Fatalf("supplement = %+v", service.settlements[1])
	}
	must(t, service.DisputeBill(2, 2))
	if len(service.settlements[1]) != 3 || settlementTotal(service.settlements[1][2]) != 2 {
		t.Fatalf("dispute reversal supplement = %+v", service.settlements[1])
	}
	must(t, service.AdjudicateBill(2, 2, 4))
	if len(service.settlements[1]) != 4 || settlementTotal(service.settlements[1][3]) != -1 {
		t.Fatalf("adjudication supplement = %+v", service.settlements[1])
	}
	if sumShares(service.allocations[2]) != 4 {
		t.Fatalf("adjudicated allocation = %d", sumShares(service.allocations[2]))
	}
}

func TestRejectionOrderAndNoTrace(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 5, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.AddTenant(MoveInput{Now: 5, TenantID: 2, RoomID: 2, Day: 0}))
	must(t, service.MoveOut(MoveInput{Now: 5, TenantID: 1, RoomID: 1, Day: 1}))
	cases := []error{
		service.AddTenant(MoveInput{Now: -1, TenantID: 0, RoomID: 0, Day: -1}),
		service.AddTenant(MoveInput{Now: 4, TenantID: 9, RoomID: 9, Day: 0}),
		service.AddTenant(MoveInput{Now: 5, TenantID: 9, RoomID: 9, Day: 0}),
		service.AddTenant(MoveInput{Now: 5, TenantID: 3, RoomID: 2, Day: 0}),
		service.MoveOut(MoveInput{Now: 5, TenantID: 1, RoomID: 1, Day: 1}),
		service.MoveOut(MoveInput{Now: 6, TenantID: 1, RoomID: 1, Day: 5}),
	}
	want := []ErrorCode{ErrInvalidArgument, ErrClockRewind, ErrNotFound, ErrOverlap, ErrInvalidState, ErrInvalidState}
	for index, err := range cases {
		if code(err) != want[index] {
			t.Fatalf("case %d code = %s, want %s", index, code(err), want[index])
		}
	}
	if len(service.tenants) != 2 || len(service.bills) != 0 || service.lastNow != 5 {
		t.Fatalf("rejected operation changed state: tenants=%d bills=%d now=%d", len(service.tenants), len(service.bills), service.lastNow)
	}
}

func TestBillErrorsAndDisputeWindow(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	if err := service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 0, Start: 0, End: 1}); code(err) != ErrInvalidArgument {
		t.Fatalf("zero amount code = %s", code(err))
	}
	if err := service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 1_000_000_001, Start: 0, End: 1}); code(err) != ErrAmountRange {
		t.Fatalf("large amount code = %s", code(err))
	}
	must(t, service.AddBill(BillInput{Now: 0, BillID: 1, Amount: 2, Start: 0, End: 1, PayerID: 1}))
	if err := service.DisputeBill(3, 1); code(err) != ErrInvalidState {
		t.Fatalf("late dispute code = %s", code(err))
	}
	must(t, service.DisputeBill(2, 1))
	if err := service.DisputeBill(2, 1); code(err) != ErrInvalidState {
		t.Fatalf("repeat dispute code = %s", code(err))
	}
	if err := service.AdjudicateBill(2, 1, 3); code(err) != ErrAmountRange {
		t.Fatalf("over-original adjudication code = %s", code(err))
	}
	must(t, service.AdjudicateBill(2, 1, 1))
	if err := service.AdjudicateBill(2, 1, 1); code(err) != ErrInvalidState {
		t.Fatalf("repeat adjudication code = %s", code(err))
	}
}

func TestConcurrentAddAndMoveOutAreSerialized(t *testing.T) {
	service := testService()
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}))
	must(t, service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 0}))
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Add(2)
		billID := 100 + i
		go func() {
			defer wait.Done()
			_ = service.AddBill(BillInput{Now: 1, BillID: billID, Amount: 10, Start: 0, End: 1, PayerID: 1})
		}()
		go func() {
			defer wait.Done()
			_ = service.MoveOut(MoveInput{Now: 1, TenantID: 2, RoomID: 2, Day: 1})
		}()
	}
	wait.Wait()
	total := 0
	for _, shares := range service.allocations {
		total += sumShares(shares)
	}
	settledTotal := 0
	for _, settlement := range service.settlements[2] {
		settledTotal += settlementTotal(settlement)
	}
	expectedNet := 0
	for _, value := range service.settlementNet(2, 1) {
		expectedNet -= value
	}
	if total != 200 || len(service.settlements[2]) < 1 || len(service.settlements[2]) > 21 || settledTotal != expectedNet {
		t.Fatalf("concurrent allocation=%d, settlement records=%d, settled total=%d", total, len(service.settlements[2]), settledTotal)
	}
}

func BenchmarkNetBetween(b *testing.B) {
	for _, bills := range []int{100, 10_000} {
		service := testService()
		if err := service.AddTenant(MoveInput{Now: 0, TenantID: 1, RoomID: 1, Day: 0}); err != nil {
			b.Fatal(err)
		}
		if err := service.AddTenant(MoveInput{Now: 0, TenantID: 2, RoomID: 2, Day: 0}); err != nil {
			b.Fatal(err)
		}
		for billID := 1; billID <= bills; billID++ {
			if err := service.AddBill(BillInput{Now: 0, BillID: billID, Amount: 1, Start: 0, End: 1, PayerID: 1}); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("bills-%d", bills), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := service.NetBetween(1, 2); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func sumShares(shares []Share) int {
	total := 0
	for _, share := range shares {
		total += share.Amount
	}
	return total
}

func amountFor(shares []Share, tenantID int) int {
	total := 0
	for _, share := range shares {
		if share.TenantID == tenantID {
			total += share.Amount
		}
	}
	return total
}

func sumTenantBalances(net map[edgeKey]int, tenantIDs ...int) int {
	total := 0
	for _, tenantID := range tenantIDs {
		for key, value := range net {
			if key.a == tenantID {
				total += value
			}
			if key.b == tenantID {
				total -= value
			}
		}
	}
	return total
}

func settlementTotal(settlement *Settlement) int {
	total := 0
	for _, edge := range settlement.Edges {
		if edge.ToID == settlement.TenantID {
			total += edge.Amount
		} else {
			total -= edge.Amount
		}
	}
	return total
}
