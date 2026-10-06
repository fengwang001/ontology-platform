package qc

import "testing"

func benchmarkRunsAtHistory(scale int, b *testing.B) {
	spec := testSpec()
	system := NewSystem()
	_ = system.RegisterAssay(0, spec)
	for index := 0; index < scale; index++ {
		_ = system.Calibrate(int64(index+1), spec.InstrumentID, spec.AssayID)
		runAt(b, system, int64(index+1), 0, 0)
	}
	input := RunInput{InstrumentID: spec.InstrumentID, AssayID: spec.AssayID, LowValue: 0, HighValue: 0}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := system.SubmitRun(int64(scale+index+1), input); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRunsHistory1k(b *testing.B) {
	benchmarkRunsAtHistory(1_000, b)
}

func BenchmarkRunsHistory100k(b *testing.B) {
	benchmarkRunsAtHistory(100_000, b)
}

func benchmarkRetrospectiveAtScale(history, pending int, b *testing.B) {
	spec := testSpec()
	input := RunInput{InstrumentID: spec.InstrumentID, AssayID: spec.AssayID, LowValue: 0, HighValue: 0}
	failure := RunInput{InstrumentID: spec.InstrumentID, AssayID: spec.AssayID, LowValue: 31, HighValue: 0}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		system := NewSystem()
		_ = system.RegisterAssay(0, spec)
		_, _ = system.SubmitRun(1, input)
		for index := 0; index < history; index++ {
			_ = system.IssueReport(2, "old-"+itoo(iteration, index), spec.InstrumentID, spec.AssayID)
		}
		_ = system.Calibrate(3, spec.InstrumentID, spec.AssayID)
		_, _ = system.SubmitRun(4, input)
		for index := 0; index < pending; index++ {
			_ = system.IssueReport(5, "new-"+itoo(iteration, index), spec.InstrumentID, spec.AssayID)
		}
		b.StartTimer()
		if _, err := system.SubmitRun(6, failure); err != nil {
			b.Fatal(err)
		}
	}
}

func itoo(iteration, index int) string {
	return itoa(iteration) + "-" + itoa(index)
}

func BenchmarkRetrospective100History10Pending(b *testing.B) {
	benchmarkRetrospectiveAtScale(100, 10, b)
}

func BenchmarkRetrospective10000History10Pending(b *testing.B) {
	benchmarkRetrospectiveAtScale(10_000, 10, b)
}
