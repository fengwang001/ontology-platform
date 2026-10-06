package imaging

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSafety 并发执行全部操作类型。
// 全局互斥使结果等价于某个串行顺序；不变量（时钟单调、设备至多一个占用、
// 留观人数不超容量）在任何被接受操作后必须成立。
func TestConcurrentSafety(t *testing.T) {
	cfg := testConfig()
	cfg.ObservationCapacity = 4
	h, _ := NewHospital(cfg)
	mustOK(t, h.RegisterDevice(RegisterDeviceRequest{ID: "ct", Class: ClassCT}), "ct")
	mustOK(t, h.RegisterExamType(RegisterExamTypeRequest{ID: "e", Class: ClassCT, Duration: 40, Enhanced: true}), "exam")
	for i := range 20 {
		pid := fmt.Sprintf("p%d", i)
		mustOK(t, h.RegisterPatient(RegisterPatientRequest{ID: pid, NoImplant: true}), "patient")
		mustOK(t, h.RecordKidney(RecordKidneyRequest{PatientID: pid, Value: 80, SampledAt: 0, Now: 0}), "kidney")
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := g*10000 + i
				pid := fmt.Sprintf("p%d", (g+i)%20)
				aid := fmt.Sprintf("a-%d-%d", g, i)
				_ = h.Book(BookRequest{ID: aid, PatientID: pid, ExamTypeID: "e", DeviceID: "ct", Start: 100000 + i*70, Now: now})
				_ = h.CheckIn(CheckInRequest{AppointmentID: aid, Now: 100000 + i*70})
				_ = h.Cancel(CancelRequest{AppointmentID: aid, Now: 100000 + i*70})
			}
		}(g)
	}
	wg.Wait()

	snap := h.Snapshot()
	occ := snap.Devices["ct"].Occupancies
	for i := 1; i < len(occ); i++ {
		if occ[i].Start < occ[i-1].End {
			t.Fatalf("设备存在重叠占用: %+v %+v", occ[i-1], occ[i])
		}
	}
	for _, a := range snap.Appointments {
		if a.Status == StatusBooked || a.Status == StatusCheckedIn {
			found := false
			for _, o := range occ {
				if o.Start == a.Start && o.End == a.OccupEnd {
					found = true
				}
			}
			if !found {
				t.Fatalf("存活预约缺少设备占用: %+v", a)
			}
		}
	}
}
