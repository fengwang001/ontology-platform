package staffing

import (
	"fmt"
	"testing"
)

func BenchmarkIssueLookupWithLargeHistory(b *testing.B) {
	for _, history := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("history_%d", history), func(b *testing.B) {
			s := NewService(0, 0)
			if err := s.AddCandidate(0, "fresh"); err != nil {
				b.Fatal(err)
			}
			if err := s.AddCandidate(0, "target-candidate"); err != nil {
				b.Fatal(err)
			}
			if err := s.AddPosition(0, Position{
				ID:        "target",
				Level:     "L",
				MinSalary: 100,
				MaxSalary: 100,
				Total:     1,
			}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < history; i++ {
				candidateID := "history" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676%26))
				if err := s.AddCandidate(0, candidateID); err != nil {
					b.Fatal(err)
				}
				if err := s.AddPosition(0, Position{
					ID:        "history" + candidateID,
					Level:     "L",
					MinSalary: 1,
					MaxSalary: 1,
					Total:     1,
				}); err != nil {
					b.Fatal(err)
				}
				if _, err := s.IssueOffer(IssueOfferInput{
					Now:         0,
					OfferID:     "history-offer-" + candidateID,
					CandidateID: candidateID,
					PositionID:  "history" + candidateID,
					Salary:      1,
					Deadline:    10,
				}); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := s.IssueOffer(IssueOfferInput{
				Now:         0,
				OfferID:     "target-occupant",
				CandidateID: "target-candidate",
				PositionID:  "target",
				Salary:      100,
				Deadline:    10,
			}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := s.IssueOffer(IssueOfferInput{
					Now:         0,
					OfferID:     fmt.Sprintf("fresh-offer-%d", i),
					CandidateID: "fresh",
					PositionID:  "target",
					Salary:      100,
					Deadline:    10,
				})
				if err == nil {
					b.Fatal("expected full-capacity rejection")
				}
			}
		})
	}
}
