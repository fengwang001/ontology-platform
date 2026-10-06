package pvbinding

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentImmediateBindings(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	const volumeCount = 50
	const claimCount = 100
	for i := 0; i < volumeCount; i++ {
		name := fmt.Sprintf("pv-%02d", i)
		requireOK(t, controller, log, "CreateVolume("+name+")", controller.CreateVolume(testVolume(name, 1, "c")), "创建并发争抢的卷")
	}

	var wg sync.WaitGroup
	results := make(chan string, claimCount)
	for i := 0; i < claimCount; i++ {
		name := fmt.Sprintf("claim-%02d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := controller.CreateClaim(testClaim(name, 1, "c", BindingImmediate))
			if err != nil {
				results <- name + ":error:" + err.Error()
			} else {
				results <- name + ":ok"
			}
		}()
	}
	wg.Wait()
	close(results)

	bound := make(map[string]string)
	for result := range results {
		log.record("Concurrent CreateClaim", nil, result)
	}
	for i := 0; i < claimCount; i++ {
		name := fmt.Sprintf("claim-%02d", i)
		volume := boundVolume(t, controller, name)
		if volume != "" {
			if other, exists := bound[volume]; exists {
				t.Fatalf("volume %s bound to %s and %s", volume, other, name)
			}
			bound[volume] = name
		}
	}
	if len(bound) != volumeCount {
		t.Fatalf("bound count = %d, want %d", len(bound), volumeCount)
	}
	if err := controller.CheckConsistency(); err != nil {
		t.Fatal(err)
	}
}

func TestImmediateSelectionIgnoresUnrelatedStorageClasses(t *testing.T) {
	log := newOperationLog(t)
	controller := NewController()
	const unrelatedCount = 10000
	for i := 0; i < unrelatedCount; i++ {
		name := fmt.Sprintf("unrelated-%05d", i)
		if err := controller.CreateVolume(testVolume(name, 1, "unrelated")); err != nil {
			t.Fatal(err)
		}
	}
	for _, spec := range []VolumeSpec{
		testVolume("related-b", 1, "related"),
		testVolume("related-a", 1, "related"),
	} {
		requireOK(t, controller, log, "CreateVolume("+spec.Name+")", controller.CreateVolume(spec), "创建同存储类候选")
	}

	controller.mu.Lock()
	resetCandidateCheckCount()
	record := &claimRecord{claim: Claim{
		Name:              "probe",
		RequestedCapacity: 1,
		StorageClass:      "related",
		AccessModes:       map[string]struct{}{"RWO": {}},
	}}
	selected := controller.chooseImmediate(record)
	observed := candidateCheckCount
	controller.mu.Unlock()

	log.record(fmt.Sprintf("chooseImmediate(%s), unrelated=%d", "probe", unrelatedCount), nil, fmt.Sprintf("候选检查数=%d，只遍历related存储类的2个卷", observed))
	if selected == nil || selected.volume.Name != "related-a" {
		t.Fatalf("selected %v", selected)
	}
	if observed != 2 {
		t.Fatalf("candidate checks = %d, want 2", observed)
	}
}
