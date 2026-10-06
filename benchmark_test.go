package battery

import "testing"

func BenchmarkSubmitConstantState(b *testing.B) {
	manager, err := NewManager(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	temperatures := validTemperatures(50, 50)
	voltages := validVoltages()
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		sample := sampleAt(int64(iteration+1), voltages, temperatures, 0)
		if _, err := manager.Submit(sample); err != nil {
			b.Fatal(err)
		}
	}
}
