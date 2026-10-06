package presence

import "testing"

// BenchmarkReportScale proves Report cost is independent of total user count:
// the ns/op here must stay comparable to BenchmarkReport despite 100x users.
func BenchmarkReportScale(b *testing.B) {
	svc, _ := New(Config{LeaseSeconds: 3600})
	for i := 0; i < 1_000_000; i++ {
		if err := svc.Report("noise"+itoa(i), "d", Online, 0); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := svc.Report("hot", "d", Online, int64(i)+1); err != nil {
			b.Fatal(err)
		}
	}
}
