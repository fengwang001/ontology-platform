package lease

import (
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentIssueAndAcceptAreLinearizable(t *testing.T) {
	service := newTestService(t)
	createTestLease(t, service, 0, baseLease())

	var waitGroup sync.WaitGroup
	successes := make(chan Snapshot, 32)
	for i := 0; i < cap(successes); i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			snapshot, err := service.IssueOffer(310, IssueOfferInput{LeaseID: "L1", RentCents: 100000, NewEndDay: 800})
			if err == nil {
				successes <- snapshot
			}
		}()
	}
	waitGroup.Wait()
	close(successes)

	if len(successes) != 1 {
		t.Fatalf("successful concurrent issues = %d, want 1", len(successes))
	}
	offerID := (<-successes).CurrentOffer.ID

	const attempts = 64
	var accepted sync.WaitGroup
	acceptCount := make(chan struct{}, attempts)
	for i := 0; i < attempts; i++ {
		accepted.Add(1)
		go func() {
			defer accepted.Done()
			if _, err := service.TenantRespond(311, RespondInput{LeaseID: "L1", OfferID: offerID, Decision: Accept}); err == nil {
				acceptCount <- struct{}{}
			}
		}()
	}
	accepted.Wait()
	close(acceptCount)

	if len(acceptCount) != 1 {
		t.Fatalf("successful concurrent accepts = %d, want 1", len(acceptCount))
	}
}

func BenchmarkSnapshotWithManyLeases(b *testing.B) {
	for _, leaseCount := range []int{10, 1000, 10000} {
		b.Run(strconv.Itoa(leaseCount), func(b *testing.B) {
			service := newTestService(b)
			for i := 0; i < leaseCount; i++ {
				id := "L" + strconv.Itoa(i)
				input := baseLease()
				input.ID = id
				if _, err := service.CreateLease(0, input); err != nil {
					b.Fatal(err)
				}
			}
			target := "L0"
			if _, err := service.IssueOffer(310, IssueOfferInput{LeaseID: target, RentCents: 100000, NewEndDay: 800}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := service.Snapshot(311, target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
