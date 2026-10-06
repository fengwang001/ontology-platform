package thinpool

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentWritesAreSerialized(t *testing.T) {
	const volumeCount = 8
	const blocksPerVolume = 8
	pool, err := NewPool(volumeCount*blocksPerVolume, 100, 90, 95)
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < volumeCount; index++ {
		name := fmt.Sprintf("v%d", index)
		if err := pool.CreateVolume(name, blocksPerVolume, 0); err != nil {
			t.Fatal(err)
		}
	}

	var wait sync.WaitGroup
	for volumeIndex := 0; volumeIndex < volumeCount; volumeIndex++ {
		for block := uint64(0); block < blocksPerVolume; block++ {
			wait.Add(1)
			go func(volumeIndex int, block uint64) {
				defer wait.Done()
				name := fmt.Sprintf("v%d", volumeIndex)
				if err := pool.WriteBlock(name, block); err != nil {
					t.Errorf("write %s[%d]: %v", name, block, err)
				}
			}(volumeIndex, block)
		}
	}
	wait.Wait()

	snapshot := pool.Snapshot()
	if snapshot.Allocated != volumeCount*blocksPerVolume {
		t.Fatalf("allocated=%d, want %d", snapshot.Allocated, volumeCount*blocksPerVolume)
	}
	if snapshot.Free != 0 || snapshot.TotalDeficit != 0 {
		t.Fatalf("unexpected free/deficit: %+v", snapshot)
	}
	for index := 0; index < volumeCount; index++ {
		name := fmt.Sprintf("v%d", index)
		if used := snapshot.Volumes[name].Used; used != blocksPerVolume {
			t.Fatalf("%s used=%d, want %d", name, used, blocksPerVolume)
		}
	}
}
