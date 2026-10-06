package presence

import "testing"

// BenchmarkReport measures an accepted heartbeat. It must stay constant in
// the number of users and subscriptions.
func BenchmarkReport(b *testing.B) {
	svc, _ := New(Config{LeaseSeconds: 3600})
	// Pre-create a large unrelated user base so Report must not scan them.
	for i := 0; i < 10000; i++ {
		u := "noise" + itoa(i)
		if err := svc.Report(u, "d", Online, 0); err != nil {
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

// BenchmarkDrain measures a drain for one viewer with a handful of
// subscriptions while thousands of unrelated users exist.
func BenchmarkDrain(b *testing.B) {
	svc, _ := New(Config{LeaseSeconds: 3600})
	for i := 0; i < 10000; i++ {
		if err := svc.Report("noise"+itoa(i), "d", Online, 0); err != nil {
			b.Fatal(err)
		}
	}
	benchMust(b, svc.Report("viewer", "d", Online, 0))
	for i := 0; i < 8; i++ {
		t := "target" + itoa(i)
		benchMust(b, svc.Report(t, "d", Online, 0))
		benchMust(b, svc.Subscribe("viewer", t, 0))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Drain("viewer", int64(i)+1); err != nil {
			b.Fatal(err)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

func benchMust(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
