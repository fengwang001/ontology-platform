package pumpstation

import "testing"

func BenchmarkReportLevelHistory100(b *testing.B) {
	benchmarkReportLevel(b, 100)
}

func BenchmarkReportLevelHistory100000(b *testing.B) {
	benchmarkReportLevel(b, 100_000)
}

func benchmarkReportLevel(b *testing.B, history int64) {
	controller, err := New(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	for i := int64(0); i < history; i++ {
		if _, err := controller.ReportLevel(i, 10); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := int64(0); i < int64(b.N); i++ {
		now := history + i
		if _, err := controller.ReportLevel(now, 10); err != nil {
			b.Fatal(err)
		}
	}
}
