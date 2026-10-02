package signals

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	s, _ := New(20, 0)
	for range 7 {
		if _, err := s.AddThread(0); err != nil {
			t.Fatal(err)
		}
	}
	for sig := 10; sig <= 16; sig++ {
		if err := s.SetAction(sig, Action{
			Kind:           ActionHandler,
			HandlerID:      100 + sig,
			AdditionalMask: bit(13),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for sig := 40; sig <= 43; sig++ {
		if err := s.SetAction(sig, Action{Kind: ActionHandler, HandlerID: 200 + sig}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 300; iteration++ {
				tid := 1 + (worker+iteration)%8
				switch (worker + iteration) % 9 {
				case 0:
					_, _ = s.Send(10+(worker+iteration)%7, iteration)
				case 1:
					_, _ = s.SendTo(tid, 10+(worker+iteration)%7, iteration)
				case 2:
					_, _ = s.Send(40+(worker+iteration)%4, iteration)
				case 3:
					_, _ = s.SendTo(tid, 40+(worker+iteration)%4, iteration)
				case 4:
					_, _ = s.Deliver(tid)
				case 5:
					_ = s.Sigreturn(tid)
				case 6:
					_ = s.SetMask(tid, bit(11))
				case 7:
					_, _ = s.Pending(tid)
					_ = s.SharedPending()
				default:
					_ = s.RTQ()
					_ = s.Curr()
					_ = s.Lost()
					_ = s.Examined()
				}
			}
		}(worker)
	}
	wg.Wait()

	if got := s.RTQ(); got < 0 || got > 20 {
		t.Fatalf("invalid RTQ after concurrent run: %d", got)
	}
}
