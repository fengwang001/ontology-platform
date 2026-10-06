package ontology

import "context"

func (s *Service) AddVehicle(_ context.Context, at Time, in VehicleInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validConfig(s.cfg) || in.ID == "" || in.Seats < 0 || in.Position < 0 || in.CurrentPassengers < 0 || in.CurrentPassengers > in.Seats {
		return ErrInvalidArgument
	}
	if at < s.clock {
		return ErrClockRollback
	}
	if _, exists := s.vehicles[in.ID]; exists {
		return ErrInvalidArgument
	}
	s.clock = at
	s.vehicles[in.ID] = &vehicle{input: in, lastTime: at, orderIDs: map[string]struct{}{}}
	s.record("add_vehicle", at, map[string]any{"vehicle": in}, nil, nil, "registered")
	return nil
}

func (s *Service) SubmitOrder(_ context.Context, in OrderInput) (result SubmitResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason := "rejected before state change"
	defer func() { s.record("submit_order", in.BookTime, map[string]any{"order": in}, result, err, reason) }()
	if !validOrderInput(in) {
		return result, ErrInvalidArgument
	}
	if in.BookTime < s.clock {
		return result, ErrClockRollback
	}
	if _, exists := s.orders[in.ID]; exists {
		return result, ErrInvalidArgument
	}
	s.clock = in.BookTime
	ord := &order{view: OrderView{ID: in.ID, Status: StatusWaiting, Solo: soloPriceInput(in, s.cfg)}, input: in, bookIndex: s.nextIndex()}
	s.orders[in.ID] = ord
	if vid, ok := s.findVehicle(ord, in.BookTime); ok {
		s.assign(ord, vid, in.BookTime)
		result = SubmitResult{OrderID: in.ID, Status: ord.view.Status, VehicleID: vid, Payable: ord.view.Payable, Cap: ord.view.Cap}
		reason = "selected minimum delay increase; locked price"
	} else {
		s.waiting = append(s.waiting, in.ID)
		result = SubmitResult{OrderID: in.ID, Status: StatusWaiting, Solo: ord.view.Solo}
		err = ErrNoVehicleAvailable
	}
	return result, err
}

func (s *Service) CancelOrder(_ context.Context, orderID string, at Time) (result CancelResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason := "rejected before state change"
	defer func() { s.record("cancel_order", at, map[string]any{"order": orderID}, result, err, reason) }()
	if orderID == "" {
		return result, ErrInvalidArgument
	}
	if at < s.clock {
		return result, ErrClockRollback
	}
	ord := s.orders[orderID]
	if ord == nil {
		return result, ErrOrderNotFound
	}
	if ord.view.Status == StatusCompleted {
		return result, ErrOrderCompleted
	}
	if ord.view.Status == StatusBoarded {
		return result, ErrBoardedCannotCancel
	}
	if ord.view.Status == StatusCancelled || ord.view.Status == StatusExpired {
		return result, ErrInvalidArgument
	}
	s.clock = at
	if ord.view.Status == StatusMatched {
		v := s.vehicles[ord.view.VehicleID]
		joinIndex := ord.joinIndex
		delete(v.orderIDs, orderID)
		applyCancelRepricing(v, s.orders, joinIndex, s.cfg)
	} else {
		s.removeWaiting(orderID)
	}
	ord.view.Status = StatusCancelled
	ord.view.VehicleID = ""
	ord.view.Payable = s.cfg.CancellationFee
	result = CancelResult{OrderID: orderID, Status: StatusCancelled, Fee: s.cfg.CancellationFee}
	reason = "removed before pickup; fixed fee charged"
	return CancelResult{OrderID: orderID, Status: StatusCancelled, Fee: s.cfg.CancellationFee}, nil
}

func (s *Service) GetOrder(_ context.Context, orderID string) (OrderView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return OrderView{}, ErrInvalidArgument
	}
	ord := s.orders[orderID]
	if ord == nil {
		return OrderView{}, ErrOrderNotFound
	}
	s.record("get_order", s.clock, map[string]any{"order": orderID}, ord.view, nil, "current-state map read")
	return ord.view, nil
}

func soloPriceInput(in OrderInput, cfg Config) Money {
	return Money(in.Dropoff-in.Pickup) * cfg.PricePerUnit
}
