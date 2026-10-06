package ontology

import "sort"

type MoveInput struct {
	Now      int
	TenantID int
	RoomID   int
	Day      int
}

type BillInput struct {
	Now      int
	BillID   int
	Amount   int
	Start    int
	End      int
	PayerID  int
	Landlord bool
	ByArea   bool
}

const (
	noEnd         = 1 << 30
	payerLandlord = 0
)

func validDay(day int) bool { return day >= 0 && day < noEnd }

func roomOverlaps(tenant *Tenant, roomID, start, end int) bool {
	for _, segment := range tenant.Segments {
		if segment.RoomID == roomID && start < segment.End && segment.Start < end {
			return true
		}
	}
	return false
}

func canonicalKey(first, second int) edgeKey {
	if first > second {
		first, second = second, first
	}
	return edgeKey{a: first, b: second}
}

func addEdge(net map[edgeKey]int, first, second, amount int) {
	if first == second || amount == 0 {
		return
	}
	key := canonicalKey(first, second)
	if key.a != first {
		amount = -amount
	}
	net[key] += amount
}

func edgeValue(net map[edgeKey]int, first, second int) int {
	if first == second {
		return 0
	}
	key := canonicalKey(first, second)
	value := net[key]
	if key.a != first {
		return -value
	}
	return value
}

func sortedInts[V any](values map[int]V) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func (s *Service) checkClock(now int) error {
	if now < s.lastNow {
		return fail(ErrClockRewind, "operation time is before the last accepted time")
	}
	return nil
}

func (s *Service) AddTenant(input MoveInput) error {
	if input.Now < 0 || input.TenantID <= 0 || input.RoomID <= 0 || !validDay(input.Day) {
		return fail(ErrInvalidArgument, "invalid move-in parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	room, ok := s.rooms[input.RoomID]
	if !ok {
		return fail(ErrNotFound, "room does not exist")
	}
	if room.Area <= 0 {
		return fail(ErrInvalidArgument, "room area must be positive")
	}
	if _, exists := s.tenants[input.TenantID]; exists {
		return fail(ErrInvalidState, "tenant already exists")
	}
	for _, other := range s.tenants {
		if roomOverlaps(other, input.RoomID, input.Day, noEnd) {
			return fail(ErrOverlap, "room is already occupied on the move-in day")
		}
	}
	s.tenants[input.TenantID] = &Tenant{
		ID:       input.TenantID,
		Segments: []Segment{{RoomID: input.RoomID, Start: input.Day, End: noEnd}},
	}
	s.lastNow = input.Now
	s.version++
	s.rebuildAndSettle(0)
	return nil
}

func (s *Service) MoveRoom(input MoveInput) error {
	if input.Now < 0 || input.TenantID <= 0 || input.RoomID <= 0 || !validDay(input.Day) {
		return fail(ErrInvalidArgument, "invalid room-change parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	room, ok := s.rooms[input.RoomID]
	if !ok {
		return fail(ErrNotFound, "target room does not exist")
	}
	if room.Area <= 0 {
		return fail(ErrInvalidArgument, "room area must be positive")
	}
	tenant := s.tenants[input.TenantID]
	if tenant == nil {
		return fail(ErrNotFound, "tenant does not exist")
	}
	last := tenant.Segments[len(tenant.Segments)-1]
	if last.End != noEnd {
		return fail(ErrInvalidState, "tenant has already moved out")
	}
	if input.Day < last.Start {
		return fail(ErrInvalidArgument, "room-change day precedes the current stay")
	}
	if input.RoomID == last.RoomID {
		return fail(ErrInvalidArgument, "redundant room change")
	}
	for _, other := range s.tenants {
		if other.ID != input.TenantID && roomOverlaps(other, input.RoomID, input.Day, noEnd) {
			return fail(ErrOverlap, "target room is already occupied")
		}
	}
	if input.RoomID != last.RoomID {
		last.End = input.Day
		tenant.Segments[len(tenant.Segments)-1] = last
		tenant.Segments = append(tenant.Segments, Segment{RoomID: input.RoomID, Start: input.Day, End: noEnd})
	}
	s.lastNow = input.Now
	s.version++
	s.rebuildAndSettle(0)
	return nil
}

func (s *Service) MoveOut(input MoveInput) error {
	if input.Now < 0 || input.TenantID <= 0 || !validDay(input.Day) {
		return fail(ErrInvalidArgument, "invalid move-out parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	tenant := s.tenants[input.TenantID]
	if tenant == nil {
		return fail(ErrNotFound, "tenant does not exist")
	}
	last := tenant.Segments[len(tenant.Segments)-1]
	if last.End != noEnd {
		return fail(ErrInvalidState, "tenant has already moved out")
	}
	if input.Day < last.Start {
		return fail(ErrInvalidArgument, "move-out day precedes move-in")
	}
	last.End = input.Day
	tenant.Segments[len(tenant.Segments)-1] = last
	s.rebuild()
	settledNet := s.settlementNet(input.TenantID, input.Day)
	edges := s.settlementEdges(settledNet, input.TenantID)
	s.settlements[input.TenantID] = append(s.settlements[input.TenantID], &Settlement{
		TenantID: input.TenantID, Day: input.Day, CreatedAt: input.Now, Edges: edges,
	})
	baseline := make(map[edgeKey]int, len(edges))
	for key, value := range settledNet {
		baseline[key] = value
	}
	s.settled[input.TenantID] = &settlementState{day: input.Day, createdAt: input.Now, baseline: baseline}
	s.settled[input.TenantID].seen = make(map[int]int)
	for billID := range s.allocations {
		s.settled[input.TenantID].seen[billID] = s.version
	}
	s.lastNow = input.Now
	return nil
}

func (s *Service) AddBill(input BillInput) error {
	if input.Now < 0 || input.BillID <= 0 || input.Amount <= 0 || !validDay(input.Start) || !validDay(input.End) || input.End <= input.Start {
		return fail(ErrInvalidArgument, "invalid bill parameters")
	}
	if input.Amount > 1_000_000_000 {
		return fail(ErrAmountRange, "bill amount exceeds the allowed range")
	}
	if !input.Landlord && input.PayerID <= 0 {
		return fail(ErrInvalidArgument, "tenant-paid bill requires a payer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(input.Now); err != nil {
		return err
	}
	if _, exists := s.bills[input.BillID]; exists {
		return fail(ErrInvalidState, "bill already exists")
	}
	if !input.Landlord && s.tenants[input.PayerID] == nil {
		return fail(ErrNotFound, "payer does not exist")
	}
	bill := Bill{
		ID: input.BillID, Amount: input.Amount, Start: input.Start, End: input.End,
		PayerID: input.PayerID, Landlord: input.Landlord, ByArea: input.ByArea,
		CreatedAt: input.Now, FinalAmount: input.Amount,
	}
	s.bills[bill.ID] = &bill
	s.version++
	s.lastNow = input.Now
	s.rebuildAndSettle(bill.ID)
	return nil
}

func (s *Service) DisputeBill(now, billID int) error {
	if now < 0 || billID <= 0 {
		return fail(ErrInvalidArgument, "invalid dispute parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	bill := s.bills[billID]
	if bill == nil {
		return fail(ErrNotFound, "bill does not exist")
	}
	if bill.Disputed {
		return fail(ErrInvalidState, "bill has already been disputed")
	}
	if bill.Adjudicated {
		return fail(ErrInvalidState, "bill has already been adjudicated")
	}
	if now > bill.CreatedAt+s.disputeWindow {
		return fail(ErrInvalidState, "dispute was filed after the allowed window")
	}
	s.removedShares[billID] = s.allocations[billID]
	bill.Disputed = true
	s.version++
	s.lastNow = now
	s.rebuildAndSettle(billID)
	return nil
}

func (s *Service) AdjudicateBill(now, billID, amount int) error {
	if now < 0 || billID <= 0 || amount < 0 {
		return fail(ErrInvalidArgument, "invalid adjudication parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	bill := s.bills[billID]
	if bill == nil {
		return fail(ErrNotFound, "bill does not exist")
	}
	if !bill.Disputed {
		return fail(ErrInvalidState, "bill has not been disputed")
	}
	if bill.Adjudicated {
		return fail(ErrInvalidState, "bill has already been adjudicated")
	}
	if amount > bill.Amount {
		return fail(ErrAmountRange, "adjudicated amount exceeds the original amount")
	}
	bill.Disputed = false
	bill.Adjudicated = true
	bill.FinalAmount = amount
	delete(s.removedShares, billID)
	s.version++
	s.lastNow = now
	s.rebuildAndSettle(billID)
	return nil
}

// NetBetween returns one canonical map entry. Go map lookup is O(1) average and
// never iterates bills, allocations, or settlements.
func (s *Service) NetBetween(firstID, secondID int) (int, error) {
	if firstID <= 0 || secondID <= 0 || firstID == secondID {
		return 0, fail(ErrInvalidArgument, "invalid tenant pair")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tenants[firstID] == nil || s.tenants[secondID] == nil {
		return 0, fail(ErrNotFound, "tenant does not exist")
	}
	return edgeValue(s.net, firstID, secondID), nil
}

func (s *Service) rebuild() {
	s.allocations = make(map[int][]Share, len(s.bills))
	s.net = make(map[edgeKey]int)
	for _, billID := range sortedInts(s.bills) {
		bill := s.bills[billID]
		if bill.Disputed {
			continue
		}
		shares := allocateBill(*bill, s.tenants, s.rooms)
		s.allocations[bill.ID] = shares
		payer := bill.PayerID
		if bill.Landlord {
			payer = payerLandlord
		}
		for _, share := range shares {
			debtor := share.TenantID
			if share.Landlord {
				debtor = payerLandlord
			}
			addEdge(s.net, payer, debtor, share.Amount)
		}
	}
}

func (s *Service) rebuildAndSettle(changedBill int) {
	s.rebuild()
	for _, tenantID := range sortedInts(s.settled) {
		state := s.settled[tenantID]
		if state.seen == nil {
			state.seen = make(map[int]int)
		}
		if changedBill != 0 && state.seen[changedBill] == s.version {
			continue
		}
		settledNet := s.settlementNet(tenantID, state.day)
		changedNet := settledNet
		for otherID := range s.tenants {
			key := canonicalKey(tenantID, otherID)
			if _, exists := state.baseline[key]; !exists {
				state.baseline[key] = 0
			}
		}
		if changedBill != 0 {
			changedNet = s.billSettlementNet(tenantID, state.day, changedBill)
			if s.bills[changedBill].Disputed {
				for key, value := range changedNet {
					changedNet[key] = -value
				}
			}
		} else {
			changedNet = make(map[edgeKey]int)
			for otherID := range s.tenants {
				if otherID == tenantID {
					continue
				}
				key := canonicalKey(tenantID, otherID)
				delta := edgeValue(settledNet, tenantID, otherID) - state.baseline[key]
				if delta != 0 {
					changedNet[key] = delta
				}
			}
		}
		edges := make([]Edge, 0)
		for _, otherID := range sortedInts(s.tenants) {
			if otherID == tenantID {
				continue
			}
			key := canonicalKey(tenantID, otherID)
			current := edgeValue(settledNet, tenantID, otherID)
			if changedNet == nil {
				continue
			}
			delta := edgeValue(changedNet, tenantID, otherID)
			if delta == 0 {
				continue
			}
			fromID := otherID
			toID := tenantID
			if delta < 0 {
				fromID, toID, delta = tenantID, otherID, -delta
			}
			edges = append(edges, Edge{FromID: fromID, ToID: toID, Amount: delta})
			state.baseline[key] = current
		}
		if changedBill != 0 {
			state.seen[changedBill] = s.version
		}
		if len(edges) > 0 {
			s.settlements[tenantID] = append(s.settlements[tenantID], &Settlement{
				TenantID: tenantID, Day: state.day, CreatedAt: s.lastNow, Supplement: true, Edges: edges,
			})
		}
	}
}

func (s *Service) billSettlementNet(tenantID, day, billID int) map[edgeKey]int {
	settledNet := make(map[edgeKey]int)
	bill := s.bills[billID]
	if bill == nil || bill.Landlord {
		return settledNet
	}
	shares := s.allocations[billID]
	if bill.Disputed {
		shares = s.removedShares[billID]
	}
	for _, share := range shares {
		if share.Landlord || share.Day >= day || (bill.PayerID != tenantID && share.TenantID != tenantID) {
			continue
		}
		addEdge(settledNet, bill.PayerID, share.TenantID, share.Amount)
	}
	return settledNet
}

func (s *Service) settlementNet(tenantID, day int) map[edgeKey]int {
	settledNet := make(map[edgeKey]int)
	for billID, shares := range s.allocations {
		bill := s.bills[billID]
		if bill.Landlord {
			continue
		}
		for _, share := range shares {
			if share.Landlord || share.Day >= day {
				continue
			}
			if bill.PayerID != tenantID && share.TenantID != tenantID {
				continue
			}
			addEdge(settledNet, bill.PayerID, share.TenantID, share.Amount)
		}
	}
	return settledNet
}

func (s *Service) settlementEdges(settledNet map[edgeKey]int, tenantID int) []Edge {
	edges := make([]Edge, 0)
	for _, otherID := range sortedInts(s.tenants) {
		if otherID == tenantID {
			continue
		}
		amount := edgeValue(s.net, tenantID, otherID)
		if amount == 0 {
			continue
		}
		fromID := otherID
		toID := tenantID
		if amount < 0 {
			fromID, toID, amount = tenantID, otherID, -amount
		}
		edges = append(edges, Edge{FromID: fromID, ToID: toID, Amount: amount})
	}
	return edges
}
