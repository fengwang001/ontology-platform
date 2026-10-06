package routewatch_test

import (
	"errors"
	"testing"

	"ontology/routewatch"
)

type leg struct {
	from, to string
	d        int64
}

func newCfg(depart int64, stops []routewatch.Stop, legs []leg, maxDrive, rest, debounce, lock int64) routewatch.Config {
	t := &routewatch.TravelTable{DepotID: "DEPOT", Duration: map[[2]string]int64{}}
	for _, l := range legs {
		t.Duration[[2]string{l.from, l.to}] = l.d
	}
	return routewatch.Config{
		RouteID:         "R1",
		DepotID:         "DEPOT",
		Departure:       depart,
		Stops:           stops,
		Travel:          *t,
		MaxDriving:      maxDrive,
		RestDuration:    rest,
		DebounceSeconds: debounce,
		LockWindow:      lock,
	}
}

func stop(id string, kind routewatch.StopKind, earliest, latest, service int64) routewatch.Stop {
	return routewatch.Stop{
		ID:      id,
		Kind:    kind,
		Window:  routewatch.Window{Earliest: earliest, Latest: latest},
		Service: service,
	}
}

func legSet(pairs ...any) []leg {
	out := []leg{}
	for i := 0; i < len(pairs); i += 3 {
		out = append(out, leg{
			from: pairs[i].(string),
			to:   pairs[i+1].(string),
			d:    pairs[i+2].(int64),
		})
	}
	return out
}

func mustNew(t *testing.T, cfg routewatch.Config) *routewatch.Monitor {
	t.Helper()
	m, err := routewatch.New(cfg)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return m
}

func resultByID(sn routewatch.Snapshot, id string) routewatch.StopResult {
	for _, r := range sn.Results {
		if r.ID == id {
			return r
		}
	}
	return routewatch.StopResult{}
}

func etaByID(sn routewatch.Snapshot, id string) (int64, bool) {
	for _, e := range sn.Published {
		if e.ID == id {
			return e.ETA, true
		}
	}
	return 0, false
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}
