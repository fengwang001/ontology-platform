package compaction

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	service := testService(t, 3, 4, 10, 2)
	var wait sync.WaitGroup

	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			key := []byte{byte('a' + index%4)}
			_ = service.RegisterFile(File{
				ID:     uint64(index + 1),
				Layer:  index % 3,
				MinKey: key,
				MaxKey: append([]byte(nil), key...),
				Bytes:  5,
			})
		}(index)
	}

	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result := service.CreatePlan()
			if result.Plan != nil {
				_ = service.CancelPlan(result.Plan.ID)
			}
		}()
	}

	wait.Wait()
	snapshot := service.Snapshot()
	if len(snapshot.Plans) != 0 || len(snapshot.Occupied) != 0 {
		t.Fatalf("pending work remains after cancellation: plans=%+v occupied=%+v", snapshot.Plans, snapshot.Occupied)
	}
}
