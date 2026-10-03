package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func testConfig(blocks int) Config {
	return Config{
		Blocks:              blocks,
		BaseConcurrency:     2,
		GlobalInflightLimit: 3,
		MaxBlockRequests:    2,
		Timeout:             10,
		FailureBanThreshold: 2,
	}
}

func mustAddPeer(t *testing.T, scheduler *BlockScheduler, id string, have ...bool) {
	t.Helper()
	if err := scheduler.AddPeer(id, have); err != nil {
		t.Fatalf("AddPeer(%q): %v", id, err)
	}
}

func TestSpecExample(t *testing.T) {
	scheduler, err := NewBlockScheduler(testConfig(4))
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true, true, true, true)
	mustAddPeer(t, scheduler, "p2", true, true, false, false)

	next := func(now int64, id string) int {
		t.Helper()
		block, ok, err := scheduler.Next(now, id)
		t.Logf("Next(now=%d id=%s) -> block=%d ok=%t err=%v; reason=rarest fresh block under global limit", now, id, block, ok, err)
		if err != nil || !ok {
			t.Fatalf("Next() = %d, %t, %v", block, ok, err)
		}
		return block
	}

	if block := next(0, "p1"); block != 2 {
		t.Fatalf("first p1 block = %d, want 2", block)
	}
	if block := next(0, "p2"); block != 0 {
		t.Fatalf("first p2 block = %d, want 0", block)
	}
	if block := next(0, "p1"); block != 3 {
		t.Fatalf("second p1 block = %d, want 3", block)
	}

	block, ok, err := scheduler.Next(0, "p2")
	t.Logf("Next(now=0 id=p2) -> block=%d ok=%t err=%v; reason=global inflight reached G", block, ok, err)
	if err != nil || ok {
		t.Fatalf("full Next() = %d, %t, %v", block, ok, err)
	}

	expired, err := scheduler.Tick(10)
	if err != nil {
		t.Fatal(err)
	}
	wantExpired := []ExpiredRequest{
		{Issued: 0, PeerID: "p1", Block: 2},
		{Issued: 0, PeerID: "p1", Block: 3},
		{Issued: 0, PeerID: "p2", Block: 0},
	}
	if !reflect.DeepEqual(expired, wantExpired) {
		t.Fatalf("Tick() = %#v, want %#v", expired, wantExpired)
	}

	if block := next(10, "p2"); block != 0 {
		t.Fatalf("p2 after timeout = %d, want 0", block)
	}
	if block := next(10, "p1"); block != 2 {
		t.Fatalf("p1 after timeout = %d, want 2", block)
	}
	block, ok, err = scheduler.Next(10, "p1")
	t.Logf("second Next(now=10 id=p1) -> block=%d ok=%t err=%v; reason=two timeouts lower p1 cap to one", block, ok, err)
	if err != nil || ok {
		t.Fatalf("p1 reduced cap Next() = %d, %t, %v", block, ok, err)
	}
}

func TestEndgameOrdersByInflightBeforeRarity(t *testing.T) {
	scheduler, err := NewBlockScheduler(Config{
		Blocks:              2,
		BaseConcurrency:     2,
		GlobalInflightLimit: 4,
		MaxBlockRequests:    2,
		Timeout:             10,
		FailureBanThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true, true)
	mustAddPeer(t, scheduler, "p2", true, true)
	mustAddPeer(t, scheduler, "p3", false, true)

	cases := []struct {
		id   string
		want int
	}{
		{"p1", 0},
		{"p3", 1},
		{"p2", 0},
	}
	for _, tc := range cases {
		block, ok, err := scheduler.Next(0, tc.id)
		t.Logf("Next id=%s -> block=%d ok=%t err=%v; reason=setup for endgame ordering", tc.id, block, ok, err)
		if err != nil || !ok || block != tc.want {
			t.Fatalf("Next(%s) = %d, %t, %v, want %d", tc.id, block, ok, err, tc.want)
		}
	}

	block, ok, err := scheduler.Next(0, "p1")
	t.Logf("endgame Next -> block=%d ok=%t err=%v; reason=minimum inflight count, then availability, then block", block, ok, err)
	if err != nil || !ok || block != 1 {
		t.Fatalf("endgame Next() = %d, %t, %v; want block 1", block, ok, err)
	}
}

func TestFreshPhasePeerWithoutFreshCandidateReturnsEmpty(t *testing.T) {
	scheduler, err := NewBlockScheduler(Config{
		Blocks:              4,
		BaseConcurrency:     2,
		GlobalInflightLimit: 4,
		MaxBlockRequests:    2,
		Timeout:             10,
		FailureBanThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true, true, true, true)
	mustAddPeer(t, scheduler, "p2", true, false, false, false)
	mustAddPeer(t, scheduler, "p3", false, true, false, false)
	mustAddPeer(t, scheduler, "p4", false, false, true, false)
	mustAddPeer(t, scheduler, "p5", false, false, false, true)

	if block, ok, err := scheduler.Next(0, "p3"); err != nil || !ok || block != 1 {
		t.Fatalf("p3 = %d, %t, %v", block, ok, err)
	}
	if block, ok, err := scheduler.Next(0, "p4"); err != nil || !ok || block != 2 {
		t.Fatalf("p4 = %d, %t, %v", block, ok, err)
	}
	if block, ok, err := scheduler.Next(0, "p5"); err != nil || !ok || block != 3 {
		t.Fatalf("p5 = %d, %t, %v", block, ok, err)
	}
	if block, ok, err := scheduler.Next(0, "p1"); err != nil || !ok || block != 0 {
		t.Fatalf("p1 duplicate block = %d, %t, %v", block, ok, err)
	}
	block, ok, err := scheduler.Next(0, "p2")
	t.Logf("p2 fresh-phase Next -> block=%d ok=%t err=%v; reason=only duplicate block 0 is available while another globally fresh block exists", block, ok, err)
	if err != nil || ok {
		t.Fatalf("p2 Next() = %d, %t, %v", block, ok, err)
	}
}

func TestTimeoutCapacityAndSuccessReset(t *testing.T) {
	scheduler, err := NewBlockScheduler(Config{
		Blocks:              4,
		BaseConcurrency:     4,
		GlobalInflightLimit: 8,
		MaxBlockRequests:    2,
		Timeout:             5,
		FailureBanThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true, true, true, true)

	for want := 0; want < 4; want++ {
		if block, ok, err := scheduler.Next(0, "p1"); err != nil || !ok || block != want {
			t.Fatalf("Next() = %d, %t, %v, want %d", block, ok, err, want)
		}
	}
	if _, err := scheduler.Tick(5); err != nil {
		t.Fatal(err)
	}
	if block, ok, err := scheduler.Next(5, "p1"); err != nil || !ok {
		t.Fatalf("after 3 timeouts Next() = %d, %t, %v", block, ok, err)
	}
	if block, ok, err := scheduler.Next(5, "p1"); err != nil || !ok {
		t.Fatalf("second after 4 timeouts Next() = %d, %t, %v", block, ok, err)
	}
	if block, ok, err := scheduler.Next(5, "p1"); err != nil || ok {
		t.Fatalf("capacity one Next() = %d, %t, %v", block, ok, err)
	}

	result, err := scheduler.Done(6, "p1", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Done(ok=true) -> banned=%t canceled=%v; reason=successful verification resets timeout count", result.Banned, result.Canceled)
	if result.Banned || len(result.Canceled) != 0 {
		t.Fatalf("Done() = %#v", result)
	}
	if block, ok, err := scheduler.Next(6, "p1"); err != nil || !ok {
		t.Fatalf("after reset Next() = %d, %t, %v", block, ok, err)
	}
}

func TestFailedBlockRequiresOtherPeerAndBanningChangesAvailability(t *testing.T) {
	scheduler, err := NewBlockScheduler(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true, true)
	mustAddPeer(t, scheduler, "p2", true, false)
	mustAddPeer(t, scheduler, "p3", false, true)

	if block, _, err := scheduler.Next(0, "p1"); err != nil || block != 0 {
		t.Fatalf("p1 first Next() = %d, %v; want block 0", block, err)
	}
	result, err := scheduler.Done(1, "p1", 0, false)
	if err != nil || result.Banned {
		t.Fatalf("first failure = %#v, %v", result, err)
	}
	if block, ok, err := scheduler.Next(1, "p3"); err != nil || !ok || block != 1 {
		t.Fatalf("p3 rare block = %d, %t, %v; want block 1", block, ok, err)
	}
	if _, ok, err := scheduler.Next(1, "p1"); err != nil || ok {
		t.Fatalf("p1 failed block in fresh phase = ok %t, err %v; want empty", ok, err)
	}
	if block, ok, err := scheduler.Next(2, "p2"); err != nil || !ok || block != 0 {
		t.Fatalf("p2 retries failed block in endgame = %d, %t, %v", block, ok, err)
	}

	if block, _, err := scheduler.Next(2, "p1"); err != nil || block != 1 {
		t.Fatalf("p1 second Next() = %d, %v; want block 1", block, err)
	}
	result, err = scheduler.Done(2, "p1", 1, false)
	if err != nil || !result.Banned {
		t.Fatalf("second failure = %#v, %v; want banned", result, err)
	}
	if _, _, err := scheduler.Next(2, "p1"); !errors.Is(err, ErrBanned) {
		t.Fatalf("banned Next error = %v, want ErrBanned", err)
	}
	if err := scheduler.Have("p1", 0); err != nil {
		t.Fatalf("Have on banned peer: %v", err)
	}
	if err := scheduler.Drop("p1"); err != nil {
		t.Fatalf("Drop banned: %v", err)
	}
	if err := scheduler.AddPeer("p1", []bool{true, true}); !errors.Is(err, ErrBanned) {
		t.Fatalf("AddPeer after banned Drop error = %v, want ErrBanned", err)
	}
}

func TestCancelReleasesSlotsAndDoneAfterCancelNoRequest(t *testing.T) {
	scheduler, err := NewBlockScheduler(Config{
		Blocks:              1,
		BaseConcurrency:     1,
		GlobalInflightLimit: 1,
		MaxBlockRequests:    2,
		Timeout:             10,
		FailureBanThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler, "p1", true)
	mustAddPeer(t, scheduler, "p2", true)

	if _, ok, err := scheduler.Next(0, "p1"); err != nil || !ok {
		t.Fatalf("p1 Next = %t, %v", ok, err)
	}
	if _, ok, err := scheduler.Next(0, "p2"); err != nil || ok {
		t.Fatalf("global full Next = %t, %v", ok, err)
	}
	if err := scheduler.Drop("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Done(1, "p1", 0, true); !errors.Is(err, ErrNoPeer) {
		t.Fatalf("Done after Drop error = %v, want ErrNoPeer", err)
	}
	if block, ok, err := scheduler.Next(1, "p2"); err != nil || !ok || block != 0 {
		t.Fatalf("p2 after release = %d, %t, %v", block, ok, err)
	}
	if _, err := scheduler.Done(2, "p2", 0, true); err != nil {
		t.Fatal(err)
	}

	scheduler2, err := NewBlockScheduler(Config{
		Blocks:              1,
		BaseConcurrency:     2,
		GlobalInflightLimit: 2,
		MaxBlockRequests:    2,
		Timeout:             10,
		FailureBanThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustAddPeer(t, scheduler2, "a", true)
	mustAddPeer(t, scheduler2, "b", true)
	if _, _, err := scheduler2.Next(0, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := scheduler2.Next(0, "b"); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler2.Done(0, "a", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Canceled, []string{"b"}) {
		t.Fatalf("canceled = %#v, want [b]", result.Canceled)
	}
	if _, err := scheduler2.Done(0, "b", 0, true); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("Done canceled request error = %v, want ErrNoRequest", err)
	}
	if !scheduler2.Complete() {
		t.Fatal("Complete() = false, want true")
	}
}

func TestErrorPrioritiesAndClock(t *testing.T) {
	scheduler, err := NewBlockScheduler(testConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.AddPeer("", []bool{true}); !errors.Is(err, ErrBadArg) {
		t.Fatalf("empty id error = %v, want ErrBadArg", err)
	}
	if err := scheduler.AddPeer("p1", []bool{}); !errors.Is(err, ErrBadArg) {
		t.Fatalf("bad length error = %v, want ErrBadArg", err)
	}
	mustAddPeer(t, scheduler, "p1", true)
	if err := scheduler.AddPeer("p1", []bool{true}); !errors.Is(err, ErrPeerExists) {
		t.Fatalf("duplicate error = %v, want ErrPeerExists", err)
	}
	if err := scheduler.Have("missing", 0); !errors.Is(err, ErrNoPeer) {
		t.Fatalf("Have missing peer error = %v, want ErrNoPeer", err)
	}
	if err := scheduler.Have("p1", 1); !errors.Is(err, ErrBadArg) {
		t.Fatalf("Have bad block error = %v, want ErrBadArg", err)
	}
	if err := scheduler.Drop("missing"); !errors.Is(err, ErrNoPeer) {
		t.Fatalf("Drop missing error = %v, want ErrNoPeer", err)
	}
	if _, _, err := scheduler.Next(1, "missing"); !errors.Is(err, ErrNoPeer) {
		t.Fatalf("Next missing error = %v, want ErrNoPeer", err)
	}
	if _, _, err := scheduler.Next(0, "p1"); !errors.Is(err, ErrClock) {
		t.Fatalf("Next backwards clock error = %v, want ErrClock", err)
	}
	if _, err := scheduler.Done(0, "p1", 0, true); !errors.Is(err, ErrClock) {
		t.Fatalf("Done backwards clock error = %v, want ErrClock", err)
	}
	if _, err := scheduler.Tick(0); !errors.Is(err, ErrClock) {
		t.Fatalf("Tick backwards clock error = %v, want ErrClock", err)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := testConfig(1)
	invalidConfigs := make([]Config, 0, 8)
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.Blocks = 0 },
		func(cfg *Config) { cfg.Blocks = 4097 },
		func(cfg *Config) { cfg.BaseConcurrency = 0 },
		func(cfg *Config) { cfg.BaseConcurrency = 17 },
		func(cfg *Config) { cfg.GlobalInflightLimit = 0 },
		func(cfg *Config) { cfg.MaxBlockRequests = 1 },
		func(cfg *Config) { cfg.Timeout = 0 },
		func(cfg *Config) { cfg.FailureBanThreshold = 0 },
	} {
		cfg := valid
		mutate(&cfg)
		invalidConfigs = append(invalidConfigs, cfg)
	}

	for i, cfg := range invalidConfigs {
		if _, err := NewBlockScheduler(cfg); !errors.Is(err, ErrBadArg) {
			t.Fatalf("invalid config %d error = %v, want ErrBadArg", i, err)
		}
	}
	if _, err := NewBlockScheduler(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}
