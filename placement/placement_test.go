package placement

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestScoreUsesLargerRemainingFractionNotSum(t *testing.T) {
	logs := &bytes.Buffer{}
	placer := NewPlacer(logs)
	mustAddNodes(t, placer, []NodeSpec{
		{ID: "a-tie", CPU: 20, Memory: 20},
		{ID: "b-lower-sum", CPU: 20, Memory: 5},
	})

	err := placer.Place(JobSpec{ID: "job", Replicas: 1, CPUPerReplica: 4, MemoryPerReplica: 4, MaxPerNode: 1})
	if err != nil {
		t.Fatalf("Place() error = %v", err)
	}

	placement, err := placer.JobPlacement("job")
	if err != nil {
		t.Fatalf("JobPlacement() error = %v", err)
	}
	if got, want := placement.Nodes, []string{"a-tie"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("placement = %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), "basis=largest_remaining_fraction_then_id") {
		t.Fatalf("logs missing scoring basis: %s", logs.String())
	}
}

func TestLaterReplicasObserveUpdatedRemainderAndTieUsesID(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{
		{ID: "b", CPU: 10, Memory: 10},
		{ID: "a", CPU: 10, Memory: 10},
	})

	err := placer.Place(JobSpec{ID: "job", Replicas: 2, CPUPerReplica: 8, MemoryPerReplica: 2, MaxPerNode: 2})
	if err != nil {
		t.Fatalf("Place() error = %v", err)
	}

	placement, _ := placer.JobPlacement("job")
	want := []string{"a", "b"}
	if len(placement.Nodes) != len(want) {
		t.Fatalf("placement = %v, want %v", placement.Nodes, want)
	}
	for index := range want {
		if placement.Nodes[index] != want[index] {
			t.Fatalf("placement = %v, want %v", placement.Nodes, want)
		}
	}
}

func TestPerNodeReplicaLimitApplies(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{
		{ID: "a", CPU: 100, Memory: 100},
		{ID: "b", CPU: 100, Memory: 100},
	})

	err := placer.Place(JobSpec{ID: "job", Replicas: 3, CPUPerReplica: 1, MemoryPerReplica: 1, MaxPerNode: 1})
	if !errors.Is(err, ErrPermanentlyUnfit) {
		t.Fatalf("Place() error = %v, want %v", err, ErrPermanentlyUnfit)
	}
	assertNodesUnchanged(t, placer, []NodeStatus{
		{ID: "a", CPU: 100, Memory: 100},
		{ID: "b", CPU: 100, Memory: 100},
	})
}

func TestGreedyFailureRollsBackEvenWhenAnotherArrangementExists(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{
		{ID: "a", CPU: 10, Memory: 10},
		{ID: "b", CPU: 10, Memory: 10},
		{ID: "c", CPU: 10, Memory: 10},
	})

	err := placer.Place(JobSpec{ID: "seed", Replicas: 2, CPUPerReplica: 1, MemoryPerReplica: 2, MaxPerNode: 2})
	if err != nil {
		t.Fatalf("seed Place() error = %v", err)
	}

	err = placer.Place(JobSpec{ID: "greedy", Replicas: 6, CPUPerReplica: 1, MemoryPerReplica: 4, MaxPerNode: 3})
	if !errors.Is(err, ErrTemporarilyInsufficient) {
		t.Fatalf("Place() error = %v, want %v", err, ErrTemporarilyInsufficient)
	}

	seed, err := placer.JobPlacement("seed")
	if err != nil {
		t.Fatalf("seed placement missing: %v", err)
	}
	if len(seed.Nodes) != 2 || seed.Nodes[0] != "a" || seed.Nodes[1] != "a" {
		t.Fatalf("seed placement changed = %v", seed.Nodes)
	}
	if _, err := placer.JobPlacement("greedy"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("failed job was recorded, error = %v", err)
	}
	assertNodesUnchanged(t, placer, []NodeStatus{
		{ID: "a", CPU: 10, Memory: 10, UsedCPU: 2, UsedMemory: 4},
		{ID: "b", CPU: 10, Memory: 10},
		{ID: "c", CPU: 10, Memory: 10},
	})
}

func TestPermanentAndTemporaryUnfitAreDistinguished(t *testing.T) {
	tests := []struct {
		name  string
		nodes []NodeSpec
		job   JobSpec
		want  error
	}{
		{
			name:  "no empty node fits one replica",
			nodes: []NodeSpec{{ID: "small", CPU: 1, Memory: 1}},
			job:   JobSpec{ID: "job", Replicas: 1, CPUPerReplica: 2, MemoryPerReplica: 1, MaxPerNode: 1},
			want:  ErrPermanentlyUnfit,
		},
		{
			name: "replica ceiling below k",
			nodes: []NodeSpec{
				{ID: "a", CPU: 10, Memory: 10},
				{ID: "b", CPU: 10, Memory: 10},
			},
			job:  JobSpec{ID: "job", Replicas: 3, CPUPerReplica: 1, MemoryPerReplica: 1, MaxPerNode: 1},
			want: ErrPermanentlyUnfit,
		},
		{
			name: "empty nodes suffice but current allocation blocks greedy",
			nodes: []NodeSpec{
				{ID: "a", CPU: 10, Memory: 10},
				{ID: "b", CPU: 10, Memory: 10},
			},
			job:  JobSpec{ID: "job", Replicas: 2, CPUPerReplica: 8, MemoryPerReplica: 8, MaxPerNode: 2},
			want: ErrTemporarilyInsufficient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placer := NewPlacer(nil)
			mustAddNodes(t, placer, tt.nodes)
			if tt.name == "empty nodes suffice but current allocation blocks greedy" {
				if err := placer.Place(JobSpec{ID: "seed", Replicas: 2, CPUPerReplica: 8, MemoryPerReplica: 8, MaxPerNode: 2}); err != nil {
					t.Fatalf("seed Place() error = %v", err)
				}
			}

			err := placer.Place(tt.job)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Place() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRemoveReleasesResourcesAndJobCanBePlacedAgain(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{{ID: "a", CPU: 5, Memory: 5}})
	job := JobSpec{ID: "job", Replicas: 1, CPUPerReplica: 5, MemoryPerReplica: 5, MaxPerNode: 1}

	if err := placer.Place(job); err != nil {
		t.Fatalf("first Place() error = %v", err)
	}
	if err := placer.Place(job); !errors.Is(err, ErrDuplicateJob) {
		t.Fatalf("duplicate Place() error = %v, want %v", err, ErrDuplicateJob)
	}
	if err := placer.Remove("job"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := placer.Remove("job"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("second Remove() error = %v, want %v", err, ErrJobNotFound)
	}
	if err := placer.Place(job); err != nil {
		t.Fatalf("second Place() error = %v", err)
	}

	assertNodesUnchanged(t, placer, []NodeStatus{{ID: "a", CPU: 5, Memory: 5, UsedCPU: 5, UsedMemory: 5}})
}

func TestValidationOrderAndRejectionLeavesStateUnchanged(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{{ID: "existing", CPU: 1, Memory: 1}})

	err := placer.AddNodes([]NodeSpec{
		{ID: "bad-capacity", CPU: 0, Memory: 1},
		{ID: "bad-capacity", CPU: 1, Memory: 1},
	})
	if !errors.Is(err, ErrInvalidNodeCapacity) {
		t.Fatalf("AddNodes() error = %v, want %v", err, ErrInvalidNodeCapacity)
	}

	err = placer.Place(JobSpec{ID: "job", Replicas: 0, CPUPerReplica: 0, MemoryPerReplica: 0, MaxPerNode: 0})
	if !errors.Is(err, ErrInvalidReplicas) {
		t.Fatalf("Place() replicas error = %v, want %v", err, ErrInvalidReplicas)
	}
	err = placer.Place(JobSpec{ID: "job", Replicas: 1, CPUPerReplica: 0, MemoryPerReplica: 0, MaxPerNode: 0})
	if !errors.Is(err, ErrInvalidResourceRequest) {
		t.Fatalf("Place() request error = %v, want %v", err, ErrInvalidResourceRequest)
	}
	err = placer.Place(JobSpec{ID: "job", Replicas: 1, CPUPerReplica: 1, MemoryPerReplica: 1, MaxPerNode: 0})
	if !errors.Is(err, ErrInvalidReplicaLimit) {
		t.Fatalf("Place() limit error = %v, want %v", err, ErrInvalidReplicaLimit)
	}

	assertNodesUnchanged(t, placer, []NodeStatus{{ID: "existing", CPU: 1, Memory: 1}})
}

func TestConcurrentPlaceRemoveAndQuery(t *testing.T) {
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, []NodeSpec{
		{ID: "a", CPU: 100, Memory: 100},
		{ID: "b", CPU: 100, Memory: 100},
	})
	job := JobSpec{ID: "job", Replicas: 4, CPUPerReplica: 5, MemoryPerReplica: 5, MaxPerNode: 2}

	if err := placer.Place(job); err != nil {
		t.Fatalf("initial Place() error = %v", err)
	}

	const workers = 12
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for index := 0; index < workers; index++ {
		go func(id int) {
			defer waitGroup.Done()
			switch id % 3 {
			case 0:
				_ = placer.Place(job)
			case 1:
				_ = placer.Remove("job")
			default:
				_, _ = placer.JobPlacement("job")
			}
			for _, node := range placer.Nodes() {
				if node.UsedCPU < 0 || node.UsedCPU > node.CPU || node.UsedMemory < 0 || node.UsedMemory > node.Memory {
					t.Errorf("node %s out of bounds: %+v", node.ID, node)
				}
			}
		}(index)
	}
	waitGroup.Wait()
}

func TestSameSequenceReplaysToSamePlacement(t *testing.T) {
	nodes := []NodeSpec{
		{ID: "c", CPU: 10, Memory: 8},
		{ID: "a", CPU: 8, Memory: 10},
		{ID: "b", CPU: 12, Memory: 12},
	}
	jobs := []JobSpec{
		{ID: "j1", Replicas: 3, CPUPerReplica: 4, MemoryPerReplica: 3, MaxPerNode: 2},
	}

	first := runDeterministicSequence(t, nodes, jobs)
	second := runDeterministicSequence(t, nodes, jobs)

	firstPlacement, err := first.JobPlacement("j1")
	if err != nil {
		t.Fatalf("first JobPlacement() error = %v", err)
	}
	secondPlacement, err := second.JobPlacement("j1")
	if err != nil {
		t.Fatalf("second JobPlacement() error = %v", err)
	}
	if len(firstPlacement.Nodes) != int(jobs[0].Replicas) || len(secondPlacement.Nodes) != int(jobs[0].Replicas) {
		t.Fatalf("replica counts = %d and %d, want %d", len(firstPlacement.Nodes), len(secondPlacement.Nodes), jobs[0].Replicas)
	}
	for index := range firstPlacement.Nodes {
		if firstPlacement.Nodes[index] != secondPlacement.Nodes[index] {
			t.Fatalf("placements differ: %v vs %v", firstPlacement.Nodes, secondPlacement.Nodes)
		}
	}

	firstNodes := first.Nodes()
	secondNodes := second.Nodes()
	for index := range firstNodes {
		if firstNodes[index] != secondNodes[index] {
			t.Fatalf("node statuses differ: %+v vs %+v", firstNodes[index], secondNodes[index])
		}
	}
}

func runDeterministicSequence(t *testing.T, nodes []NodeSpec, jobs []JobSpec) *Placer {
	t.Helper()
	placer := NewPlacer(nil)
	mustAddNodes(t, placer, nodes)
	for _, job := range jobs {
		_ = placer.Place(job)
	}
	if err := placer.Remove("j1"); err != nil {
		t.Fatalf("Remove(j1) error = %v", err)
	}
	if err := placer.Place(jobs[0]); err != nil {
		t.Fatalf("replay Place(j1) error = %v", err)
	}
	return placer
}

func mustAddNodes(t *testing.T, placer *Placer, nodes []NodeSpec) {
	t.Helper()
	if err := placer.AddNodes(nodes); err != nil {
		t.Fatalf("AddNodes(%v) error = %v", nodes, err)
	}
}

func assertNodesUnchanged(t *testing.T, placer *Placer, want []NodeStatus) {
	t.Helper()
	got := placer.Nodes()
	if len(got) != len(want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("nodes[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
}
