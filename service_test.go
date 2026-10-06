package ontology

import (
	"context"
	"errors"
	"math/rand"
	"testing"
)

func TestSkeleton(t *testing.T) {
	svc := NewService(Config{}, nil)
	if svc == nil {
		t.Fatal("nil service")
	}
}

func TestRouteDelayMergesSameStop(t *testing.T) {
	cfg := Config{StopDuration: 2, TravelTimePerUnit: 1, PricePerUnit: 1, MaxActiveOrders: 3}
	first := &order{input: OrderInput{ID: "a", Pickup: 0, Dropoff: 10, People: 1, MaxDelay: 2}}
	second := &order{input: OrderInput{ID: "b", Pickup: 4, Dropoff: 8, People: 1, MaxDelay: 2}}
	third := &order{input: OrderInput{ID: "c", Pickup: 4, Dropoff: 8, People: 1, MaxDelay: 2}}
	plan := planRoute(0, 0, 0, []*order{first, second, third}, cfg)
	if plan.delays["a"] != 4 {
		t.Fatalf("first delay = %d", plan.delays["a"])
	}
	if plan.delays["b"] != 0 || plan.delays["c"] != 0 {
		t.Fatalf("shared stops must merge: %#v", plan.delays)
	}
}

func TestRawPricesAreConserved(t *testing.T) {
	cfg := Config{PricePerUnit: 10}
	early := &order{input: OrderInput{ID: "early", BookTime: 0, Pickup: 0, Dropoff: 10, People: 1}}
	later := &order{input: OrderInput{ID: "later", BookTime: 1, Pickup: 2, Dropoff: 8, People: 1}}
	raw := rawPrices([]*order{early, later}, cfg)
	if raw["early"] != 70 || raw["later"] != 30 {
		t.Fatalf("raw = early:%d later:%d", raw["early"], raw["later"])
	}
}

func TestRandomReplayAgainstNaive(t *testing.T) {
	ctx := context.Background()
	cfg := Config{PricePerUnit: 3, StopDuration: 1, TravelTimePerUnit: 1, MaxActiveOrders: 4, CancellationFee: 5}
	logger := NewSliceLogger()
	service := NewService(cfg, logger)
	naive := NewNaiveModel(cfg)
	rng := rand.New(rand.NewSource(42))
	vehicleIDs := []string{"v1", "v2"}
	for _, id := range vehicleIDs {
		in := VehicleInput{ID: id, Seats: 3, Position: 0}
		if err := service.AddVehicle(ctx, 0, in); err != nil {
			t.Fatal(err)
		}
		if err := naive.AddVehicle(ctx, 0, in); err != nil {
			t.Fatal(err)
		}
	}
	orderSeq := 0
	knownOrders := []string{}
	clock := Time(1)
	for i := 0; i < 120; i++ {
		kind := rng.Intn(4)
		switch kind {
		case 0, 1:
			orderSeq++
			id := string(rune('a'+orderSeq%26)) + "-" + string(rune('a'+orderSeq/26))
			pickup := Position(rng.Intn(10))
			in := OrderInput{ID: id, BookTime: clock, Pickup: pickup, Dropoff: pickup + Position(1+rng.Intn(10)), People: 1 + rng.Intn(2), MaxDelay: Time(rng.Intn(4)), LatestPickup: clock + Time(rng.Intn(8))}
			r1, e1 := service.SubmitOrder(ctx, in)
			r2, e2 := naive.SubmitOrder(ctx, in)
			if !errors.Is(e1, e2) {
				t.Fatalf("op %d submit %s errors %v/%v", i, id, e1, e2)
			}
			if e1 == nil || errors.Is(e1, ErrNoVehicleAvailable) {
				knownOrders = append(knownOrders, id)
				if r1.Status != r2.Status || r1.VehicleID != r2.VehicleID || r1.Payable != r2.Payable || r1.Cap != r2.Cap {
					t.Fatalf("submit mismatch %+v vs %+v", r1, r2)
				}
			}
		case 2:
			id := vehicleIDs[rng.Intn(len(vehicleIDs))]
			current := rng.Intn(12)
			_, e1 := service.UpdatePosition(ctx, id, clock, Position(current))
			_, e2 := naive.UpdatePosition(ctx, id, clock, Position(current))
			if !errors.Is(e1, e2) {
				t.Fatalf("op %d move errors %v/%v", i, e1, e2)
			}
		case 3:
			if len(knownOrders) > 0 {
				id := knownOrders[rng.Intn(len(knownOrders))]
				_, e1 := service.CancelOrder(ctx, id, clock)
				_, e2 := naive.CancelOrder(ctx, id, clock)
				if !errors.Is(e1, e2) {
					t.Fatalf("op %d cancel errors %v/%v", i, e1, e2)
				}
			}
		}
		for _, id := range knownOrders {
			v1, err1 := service.GetOrder(ctx, id)
			v2, err2 := naive.GetOrder(ctx, id)
			if !errors.Is(err1, err2) || v1 != v2 {
				logger.Print()
				t.Fatalf("op %d order %s mismatch: (%+v,%v) vs (%+v,%v)", i, id, v1, err1, v2, err2)
			}
		}
		clock++
	}
	if logger.Len() == 0 {
		t.Fatal("logger did not record operations")
	}
	logger.Print()
}

func TestRequiredDeterministicScenarios(t *testing.T) {
	ctx := context.Background()
	cfg := Config{PricePerUnit: 1, StopDuration: 2, TravelTimePerUnit: 1, MaxActiveOrders: 10, CancellationFee: 5}

	t.Run("delay exactly at limit", func(t *testing.T) {
		localCfg := cfg
		localCfg.StopDuration = 1
		s := NewService(localCfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "v", Seats: 4, Position: 0}))
		submitOK(t, s, OrderInput{ID: "a", BookTime: 0, Pickup: 0, Dropoff: 10, People: 1, MaxDelay: 2, LatestPickup: 0})
		r, err := s.SubmitOrder(ctx, OrderInput{ID: "b", BookTime: 1, Pickup: 5, Dropoff: 8, People: 1, MaxDelay: 2, LatestPickup: 10})
		if err != nil || r.VehicleID != "v" {
			t.Fatalf("exact-limit merge failed: %+v %v", r, err)
		}
	})

	t.Run("coincident stops and exact pickup time", func(t *testing.T) {
		s := NewService(cfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "v", Seats: 4, Position: 0}))
		r := submitOK(t, s, OrderInput{ID: "a", BookTime: 0, Pickup: 4, Dropoff: 8, People: 1, MaxDelay: 0, LatestPickup: 4})
		if r.VehicleID != "v" || r.Payable != 4 {
			t.Fatalf("exact pickup failed %+v", r)
		}
	})

	t.Run("tie and exact capacity", func(t *testing.T) {
		s := NewService(cfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "b", Seats: 2, Position: 0}))
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "a", Seats: 2, Position: 0, CurrentPassengers: 1}))
		r := submitOK(t, s, OrderInput{ID: "o", BookTime: 1, Pickup: 2, Dropoff: 4, People: 1, MaxDelay: 10, LatestPickup: 10})
		if r.VehicleID != "a" {
			t.Fatalf("tie selected %s", r.VehicleID)
		}
	})

	t.Run("rounding residual and solo", func(t *testing.T) {
		s := NewService(cfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "v", Seats: 4, Position: 0}))
		submitOK(t, s, OrderInput{ID: "a", BookTime: 0, Pickup: 0, Dropoff: 10, People: 1, MaxDelay: 10, LatestPickup: 0})
		submitOK(t, s, OrderInput{ID: "b", BookTime: 0, Pickup: 0, Dropoff: 10, People: 1, MaxDelay: 10, LatestPickup: 0})
		submitOK(t, s, OrderInput{ID: "c", BookTime: 0, Pickup: 0, Dropoff: 10, People: 1, MaxDelay: 10, LatestPickup: 0})
		va := getOrder(t, s, "a")
		vb := getOrder(t, s, "b")
		vc := getOrder(t, s, "c")
		if va.Payable != 4 || vb.Payable != 3 || vc.Payable != 3 {
			t.Fatalf("allocation=%d,%d,%d", va.Payable, vb.Payable, vc.Payable)
		}
		if vb.Solo != 10 {
			t.Fatalf("solo = %d", vb.Solo)
		}
	})

	t.Run("lock decrease and cancel cap", func(t *testing.T) {
		lockCfg := cfg
		lockCfg.PricePerUnit = 10
		s := NewService(lockCfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "v", Seats: 4, Position: 0}))
		submitOK(t, s, OrderInput{ID: "a", BookTime: 0, Pickup: 0, Dropoff: 20, People: 1, MaxDelay: 10, LatestPickup: 0})
		submitOK(t, s, OrderInput{ID: "b", BookTime: 1, Pickup: 2, Dropoff: 8, People: 1, MaxDelay: 10, LatestPickup: 10})
		submitOK(t, s, OrderInput{ID: "c", BookTime: 2, Pickup: 5, Dropoff: 10, People: 1, MaxDelay: 10, LatestPickup: 10})
		a := getOrder(t, s, "a")
		c := getOrder(t, s, "c")
		if a.Cap != 200 || a.Payable >= a.Cap {
			t.Fatalf("a did not receive protected decrease: %+v", a)
		}
		if c.Payable != 20 || c.Cap != 20 {
			t.Fatalf("c initial = %+v", c)
		}
		_, err := s.CancelOrder(ctx, "b", 3)
		must(t, err)
		c = getOrder(t, s, "c")
		if c.Payable != 20 {
			t.Fatalf("cancel increase was not capped: %+v", c)
		}
	})

	t.Run("waiting match and exact later expiry", func(t *testing.T) {
		s := NewService(cfg, nil)
		must(t, s.AddVehicle(ctx, 0, VehicleInput{ID: "v", Seats: 4, Position: 0}))
		submitOK(t, s, OrderInput{ID: "road", BookTime: 0, Pickup: 0, Dropoff: 3, People: 1, MaxDelay: 10, LatestPickup: 0})
		wait(t, s, OrderInput{ID: "w", BookTime: 1, Pickup: 5, Dropoff: 7, People: 1, MaxDelay: 0, LatestPickup: 5})
		wait(t, s, OrderInput{ID: "x", BookTime: 1, Pickup: 8, Dropoff: 9, People: 1, MaxDelay: 0, LatestPickup: 5})
		move := mustMove(t, s, "v", 5, 5)
		if len(move.Matched) != 1 || move.Matched[0] != "w" {
			t.Fatalf("waiting match = %+v", move)
		}
		move = mustMove(t, s, "v", 6, 6)
		if len(move.Expired) != 1 || move.Expired[0] != "x" {
			t.Fatalf("expiry = %+v", move)
		}
	})
}

func TestErrorPriorityAndWaitingCancellation(t *testing.T) {
	ctx := context.Background()
	cfg := Config{PricePerUnit: 1, StopDuration: 1, TravelTimePerUnit: 1, MaxActiveOrders: 4, CancellationFee: 5}
	s := NewService(cfg, nil)
	must(t, s.AddVehicle(ctx, 10, VehicleInput{ID: "v", Seats: 1, Position: 0}))
	submitOK(t, s, OrderInput{ID: "r", BookTime: 10, Pickup: 0, Dropoff: 100, People: 1, MaxDelay: 0, LatestPickup: 10})
	wait(t, s, OrderInput{ID: "w", BookTime: 11, Pickup: 50, Dropoff: 60, People: 1, MaxDelay: 0, LatestPickup: 20})
	if _, err := s.UpdatePosition(ctx, "missing", 9, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid should precede clock, got %v", err)
	}
	if _, err := s.UpdatePosition(ctx, "missing", 11, 1); !errors.Is(err, ErrVehicleNotFound) {
		t.Fatalf("vehicle should precede position, got %v", err)
	}
	mustMove(t, s, "v", 11, 2)
	if _, err := s.UpdatePosition(ctx, "v", 12, 1); !errors.Is(err, ErrPositionRollback) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.CancelOrder(ctx, "r", 13); !errors.Is(err, ErrBoardedCannotCancel) {
		t.Fatalf("boarded cancel = %v", err)
	}
	r, err := s.CancelOrder(ctx, "w", 13)
	if err != nil || r.Fee != 5 {
		t.Fatalf("waiting cancel = %+v %v", r, err)
	}
	v := getOrder(t, s, "w")
	if v.Status != StatusCancelled || v.Payable != 5 {
		t.Fatalf("waiting final = %+v", v)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func submitOK(t *testing.T, s *Service, in OrderInput) SubmitResult {
	t.Helper()
	r, err := s.SubmitOrder(context.Background(), in)
	if err != nil {
		t.Fatalf("submit %s: %v", in.ID, err)
	}
	return r
}

func wait(t *testing.T, s *Service, in OrderInput) {
	t.Helper()
	if _, err := s.SubmitOrder(context.Background(), in); !errors.Is(err, ErrNoVehicleAvailable) {
		t.Fatalf("order %s should wait, err=%v", in.ID, err)
	}
}

func getOrder(t *testing.T, s *Service, id string) OrderView {
	t.Helper()
	v, err := s.GetOrder(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustMove(t *testing.T, s *Service, id string, at Time, pos Position) PositionResult {
	t.Helper()
	r, err := s.UpdatePosition(context.Background(), id, at, pos)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
