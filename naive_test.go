package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveSegment struct {
	roomID int
	start  int
	end    int
}

type naiveTenant struct {
	id       int
	segments []naiveSegment
	out      bool
}

type naiveBill struct {
	id          int
	amount      int
	start       int
	end         int
	payerID     int
	landlord    bool
	byArea      bool
	createdAt   int
	disputed    bool
	adjudicated bool
	final       int
}

type naiveModel struct {
	now    int
	rooms  map[int]int
	people map[int]*naiveTenant
	bills  map[int]*naiveBill
	window int
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		rooms:  map[int]int{1: 1, 2: 3},
		people: make(map[int]*naiveTenant),
		bills:  make(map[int]*naiveBill),
		window: 5,
	}
}

func naiveKey(first, second int) edgeKey {
	if first > second {
		first, second = second, first
	}
	return edgeKey{first, second}
}

func naiveEdge(net map[edgeKey]int, first, second int) int {
	key := naiveKey(first, second)
	value := net[key]
	if key.a != first {
		return -value
	}
	return value
}

func (model *naiveModel) net() map[edgeKey]int {
	net := make(map[edgeKey]int)
	for _, bill := range model.bills {
		if bill.disputed {
			continue
		}
		days := bill.end - bill.start
		for offset := 0; offset < days; offset++ {
			day := bill.start + offset
			amount := bill.final / days
			if offset < bill.final%days {
				amount++
			}
			type person struct {
				id     int
				moveIn int
				roomID int
				area   int
			}
			present := make([]person, 0)
			futureID := 0
			futureDay := 0
			for _, tenant := range model.people {
				for index, segment := range tenant.segments {
					moveIn := segment.start
					for trace := index; trace > 0 && model.people[tenant.id].segments[trace-1].end == model.people[tenant.id].segments[trace].start; trace-- {
						moveIn = model.people[tenant.id].segments[trace-1].start
					}
					if day >= segment.start && day < segment.end {
						present = append(present, person{tenant.id, moveIn, segment.roomID, model.rooms[segment.roomID]})
					}
					if segment.start >= day && (futureID == 0 || segment.start < futureDay || (segment.start == futureDay && segment.roomID < model.people[futureID].segments[0].roomID)) {
						futureID = tenant.id
						futureDay = segment.start
					}
				}
			}
			add := func(debtor int, value int) {
				if value == 0 {
					return
				}
				payer := bill.payerID
				if bill.landlord {
					payer = 0
				}
				if debtor == 0 {
					debtor = 0
				}
				if payer != debtor {
					key := naiveKey(payer, debtor)
					if key.a != payer {
						value = -value
					}
					net[key] += value
				}
			}
			if len(present) == 0 {
				add(futureID, amount)
				continue
			}
			for i := 0; i < len(present); i++ {
				for j := i + 1; j < len(present); j++ {
					if present[j].moveIn < present[i].moveIn || (present[j].moveIn == present[i].moveIn && present[j].roomID < present[i].roomID) {
						present[i], present[j] = present[j], present[i]
					}
				}
			}
			assigned := make([]int, len(present))
			if bill.byArea {
				totalArea := 0
				for _, item := range present {
					totalArea += item.area
				}
				left := amount
				for index, item := range present {
					assigned[index] = amount * item.area / totalArea
					left -= assigned[index]
				}
				for index := 0; index < left; index++ {
					assigned[index%len(assigned)]++
				}
			} else {
				for index := range assigned {
					assigned[index] = amount / len(present)
				}
				for index := 0; index < amount%len(present); index++ {
					assigned[index]++
				}
			}
			for index, item := range present {
				add(item.id, assigned[index])
			}
		}
	}
	return net
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	t.Parallel()
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			service := NewService([]Room{{ID: 1, Area: 1}, {ID: 2, Area: 3}}, 0, 5)
			model := newNaiveModel()
			logs := make([]string, 0)
			for step := 0; step < 60; step++ {
				now := model.now + random.Intn(3)
				tenantID := 1 + random.Intn(4)
				roomID := 1 + random.Intn(2)
				day := random.Intn(12)
				billID := 1 + random.Intn(10)
				op := random.Intn(7)
				var input, output string
				var err error
				switch op {
				case 0:
					input = fmt.Sprintf("AddTenant now=%d tenant=%d room=%d day=%d", now, tenantID, roomID, day)
					err = service.AddTenant(MoveInput{now, tenantID, roomID, day})
					if err == nil {
						model.now = now
						if model.people[tenantID] != nil {
							err = fail(ErrInvalidState, "naive duplicate")
							continue
						}
						model.people[tenantID] = &naiveTenant{id: tenantID, segments: []naiveSegment{{roomID, day, noEnd}}}
					}
				case 1:
					input = fmt.Sprintf("MoveRoom now=%d tenant=%d room=%d day=%d", now, tenantID, roomID, day)
					err = service.MoveRoom(MoveInput{now, tenantID, roomID, day})
					if err == nil {
						tenant := model.people[tenantID]
						if tenant == nil || tenant.out {
							t.Fatalf("service accepted move room rejected by naive state")
						}
						model.now = now
						if roomID != tenant.segments[len(tenant.segments)-1].roomID {
							tenant.segments[len(tenant.segments)-1].end = day
							tenant.segments = append(tenant.segments, naiveSegment{roomID, day, noEnd})
						}
					}
				case 2:
					input = fmt.Sprintf("MoveOut now=%d tenant=%d day=%d", now, tenantID, day)
					err = service.MoveOut(MoveInput{now, tenantID, roomID, day})
					if err == nil {
						tenant := model.people[tenantID]
						if tenant == nil || tenant.out {
							t.Fatalf("service accepted move out rejected by naive state")
						}
						model.now = now
						tenant.segments[len(tenant.segments)-1].end = day
						tenant.out = true
					}
				case 3:
					amount := 1 + random.Intn(15)
					landlord := random.Intn(2) == 0
					payerID := tenantID
					byArea := random.Intn(2) == 0
					end := day + 1 + random.Intn(5)
					input = fmt.Sprintf("AddBill now=%d bill=%d amount=%d range=[%d,%d) landlord=%v payer=%d byArea=%v",
						now, billID, amount, day, end, landlord, payerID, byArea)
					billInput := BillInput{
						Now: now, BillID: billID, Amount: amount, Start: day, End: end,
						PayerID: payerID, Landlord: landlord, ByArea: byArea,
					}
					err = service.AddBill(billInput)
					if err == nil {
						model.now = now
						model.bills[billID] = &naiveBill{
							id: billID, amount: billInput.Amount, start: billInput.Start, end: billInput.End,
							payerID: billInput.PayerID, landlord: billInput.Landlord, byArea: billInput.ByArea,
							createdAt: now, final: billInput.Amount,
						}
					}
				case 4:
					input = fmt.Sprintf("DisputeBill now=%d bill=%d", now, billID)
					err = service.DisputeBill(now, billID)
					if err == nil {
						model.now = now
						bill := model.bills[billID]
						if bill == nil || bill.disputed || bill.adjudicated || now > bill.createdAt+model.window {
							t.Fatalf("service accepted dispute rejected by naive state")
						}
						bill.disputed = true
					}
				default:
					amount := random.Intn(16)
					input = fmt.Sprintf("AdjudicateBill now=%d bill=%d amount=%d", now, billID, amount)
					err = service.AdjudicateBill(now, billID, amount)
					if err == nil {
						model.now = now
						bill := model.bills[billID]
						if bill == nil || !bill.disputed || bill.adjudicated || amount > bill.amount {
							t.Fatalf("service accepted adjudication rejected by naive state")
						}
						bill.disputed = false
						bill.adjudicated = true
						bill.final = amount
					}
				}
				output = "ok"
				if err != nil {
					output = "rejected:" + string(code(err))
				}
				logs = append(logs, fmt.Sprintf("step %d %s => %s; rule: serialized state transition and whole-ledger deterministic rebuild", step, input, output))
				if err != nil {
					continue
				}
				want := model.net()
				got := service.net
				for key, value := range want {
					if got[key] != value {
						t.Fatalf("net mismatch at edge (%d,%d): got %d want %d\nlogs:\n%v", key.a, key.b, got[key], value, logs)
					}
				}
				for key, value := range got {
					if want[key] != value {
						t.Fatalf("extra net edge (%d,%d): %d\nlogs:\n%v", key.a, key.b, value, logs)
					}
				}
			}
			t.Logf("operation log:\n%s", joinLogs(logs))
			for billID, bill := range service.bills {
				if !bill.Disputed && sumShares(service.allocations[billID]) != bill.FinalAmount {
					t.Fatalf("bill %d conservation failed", billID)
				}
				if model.bills[billID] == nil {
					t.Fatalf("service has bill absent from naive model")
				}
			}
		})
	}
}

func joinLogs(logs []string) string {
	result := ""
	for _, line := range logs {
		result += line + "\n"
	}
	return result
}
