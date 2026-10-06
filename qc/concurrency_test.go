package qc

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	system := NewSystem()
	var wait sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		assay := fmt.Sprintf("concurrent-%d", worker)
		config := AssayConfig{
			Low:      LevelConfig{Target: 0, SD: 100},
			High:     LevelConfig{Target: 0, SD: 100},
			ValidFor: 1_000_000_000,
		}
		if err := system.RegisterAssay(0, "analyzer", assay, config); err != nil {
			t.Fatal(err)
		}
	}
	for worker := 0; worker < 32; worker++ {
		assay := fmt.Sprintf("concurrent-%d", worker)
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 20; index++ {
				now := int64(1)
				reportID := fmt.Sprintf("w%d-r%d", worker, index)
				_, _ = system.RunQC(now, "analyzer", assay, 0, 0)
				if err := system.IssueReport(now, "analyzer", assay, reportID); err != nil {
					t.Errorf("issue report: %v", err)
					return
				}
				_, _ = system.ReportStatus(reportID)
			}
		}(worker)
	}
	wait.Wait()

	for worker := 0; worker < 32; worker++ {
		assay := fmt.Sprintf("concurrent-%d", worker)
		status, runs, _, err := system.AssaySnapshot("analyzer", assay)
		if err != nil {
			t.Fatal(err)
		}
		if status != StatusInControl || runs != 20 {
			t.Fatalf("%s status=%s runs=%d, want in_control and 20", assay, status, runs)
		}
	}
}
