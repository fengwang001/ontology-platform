package imaging

import (
	"fmt"
	"testing"
)

func benchmarkBooking(history int, b *testing.B) {
	config := testConfig()
	config.ObservationCapacity = history + b.N + 10
	system := NewSystem(config)
	must(b, system.RegisterDevice(0, Device{ID: "ct", Class: DeviceClassCT}))
	must(b, system.RegisterExam(0, ExamType{ID: "plain", DeviceClass: DeviceClassCT, Duration: 10}))
	must(b, system.RegisterPatient(0, Patient{ID: "p"}))
	for index := 0; index < history; index++ {
		start := 1000000 + index*100
		must(b, system.Book(index+1, fmt.Sprintf("h%d", index), "ct", "p", "plain", start))
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		start := 100 + index*100
		must(b, system.Book(history+index+2, fmt.Sprintf("c%d", index), "ct", "p", "plain", start))
	}
}

func BenchmarkBooking1000History(b *testing.B) { benchmarkBooking(1000, b) }

func BenchmarkBooking16000History(b *testing.B) { benchmarkBooking(16000, b) }
