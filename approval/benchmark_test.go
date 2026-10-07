package approval

import (
	"fmt"
	"testing"
)

func BenchmarkQueryOneLicense(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			service := buildBenchmarkService(b, count)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := service.QueryStage("license-0", "a", 10000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func buildBenchmarkService(b *testing.B, count int) *Service {
	b.Helper()
	definition := LicenseType{
		ID: "T",
		Stages: []StageDefinition{
			{ID: "a", Department: "A", TimeLimit: 10, CorrectionLimit: 1, CorrectionDays: 5},
			{ID: "b", Department: "B", TimeLimit: 10, Prerequisites: []string{"a"}, CorrectionLimit: 0},
		},
	}
	service, err := NewService(NewCalendar(nil), []LicenseType{definition})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("license-%d", i)
		actors := map[string]Actor{
			"actor-a": {ID: "actor-a", Department: "A"},
			"actor-b": {ID: "actor-b", Department: "B"},
		}
		err := service.Accept(AcceptRequest{LicenseID: id, TypeID: "T", ApplicantID: "applicant", Actors: actors, Day: 1})
		if err != nil {
			b.Fatal(err)
		}
	}
	return service
}
