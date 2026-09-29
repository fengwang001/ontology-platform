package reconcile

import (
	"bytes"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func bruteForceDiffs(left, right map[int][]byte, size int) []int {
	var keys []int
	for key := 0; key < size; key++ {
		lv, lok := left[key]
		rv, rok := right[key]
		switch {
		case lok != rok:
			keys = append(keys, key)
		case lok && !bytes.Equal(lv, rv):
			keys = append(keys, key)
		}
	}
	return keys
}

func TestRandomMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20260929))
	for trial := 0; trial < 200; trial++ {
		size := 1 + rng.Intn(64)
		fanout := 2 + rng.Intn(4)
		leftData := randomData(rng, size)
		rightData := cloneData(leftData)
		// mutate right with a mix of updates, inserts and deletes
		mutations := rng.Intn(size + 1)
		for i := 0; i < mutations; i++ {
			key := rng.Intn(size)
			switch rng.Intn(3) {
			case 0:
				delete(rightData, key)
			case 1:
				value := make([]byte, rng.Intn(5))
				rng.Read(value)
				if value == nil {
					value = []byte{0}
				}
				rightData[key] = value
			case 2:
				// sometimes use an explicit zero value
				rightData[key] = []byte{}
			}
		}
		left, err := NewReplicaFromData("l", size, fanout, leftData)
		if err != nil {
			t.Fatal(err)
		}
		right, err := NewReplicaFromData("r", size, fanout, rightData)
		if err != nil {
			t.Fatal(err)
		}
		report, err := Reconcile(left, right)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]int, 0, len(report.Diffs))
		for _, d := range report.Diffs {
			got = append(got, d.Key)
			lv, lok := leftData[d.Key]
			rv, rok := rightData[d.Key]
			if lok != d.LeftOK || rok != d.RightOK {
				t.Fatalf("trial %d key %d presence flags wrong", trial, d.Key)
			}
			if lok && !bytes.Equal(lv, d.Left) {
				t.Fatalf("trial %d key %d left value %x want %x", trial, d.Key, d.Left, lv)
			}
			if rok && !bytes.Equal(rv, d.Right) {
				t.Fatalf("trial %d key %d right value %x want %x", trial, d.Key, d.Right, rv)
			}
		}
		sort.Ints(got)
		want := bruteForceDiffs(leftData, rightData, size)
		if !intsEqual(got, want) {
			t.Fatalf("trial %d size=%d fanout=%d: got %v want %v", trial, size, fanout, got, want)
		}
	}
}

func TestIdenticalReplicasHaveNoDiffs(t *testing.T) {
	data := map[int][]byte{0: {1}, 3: {}, 7: {9, 9}}
	a, _ := NewReplicaFromData("a", 8, 2, data)
	b, _ := NewReplicaFromData("b", 8, 2, cloneData(data))
	report, err := Reconcile(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diffs) != 0 {
		t.Fatalf("unexpected diffs: %+v", report.Diffs)
	}
	if len(report.Sequence) != 1 || !report.Sequence[0].Equal || report.Sequence[0].DrilledDown {
		t.Fatalf("identical replicas should compare only the root: %+v", report.Sequence)
	}
}

func TestReconcileSameReplica(t *testing.T) {
	r, _ := NewReplicaFromData("solo", 4, 2, map[int][]byte{1: {2}, 3: {}})
	report, err := Reconcile(r, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diffs) != 0 || len(report.Sequence) != 1 {
		t.Fatalf("self reconcile: %+v", report)
	}
}

func TestLoggingContainsInputsDiffsAndReasons(t *testing.T) {
	var buf bytes.Buffer
	comparer := &Comparer{Writer: &buf}
	left, _ := NewReplicaFromData("left", 4, 2, map[int][]byte{0: {1}, 1: {}})
	right, _ := NewReplicaFromData("right", 4, 2, map[int][]byte{0: {2}})
	report, err := comparer.Reconcile(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diffs) != 2 {
		t.Fatalf("want 2 diffs (value + present/absent), got %+v", report.Diffs)
	}
	log := buf.String()
	for _, fragment := range []string{
		"reconcile start",
		"left=left",
		"right=right",
		"fanout=2",
		"drill down",
		"leaf mismatch, record diff",
		"present on left, absent on right",
		"both present with different values",
		"reconcile done",
		"Key:1",
	} {
		if !strings.Contains(log, fragment) {
			t.Fatalf("log missing %q\nfull log:\n%s", fragment, log)
		}
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	size, fanout := 64, 3
	left, _ := NewReplica("l", size, fanout)
	right, _ := NewReplica("r", size, fanout)
	for key := 0; key < size; key++ {
		left.Put(key, []byte{byte(key)})
		right.Put(key, []byte{byte(key)})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := rng.Intn(size)
				if rng.Intn(2) == 0 {
					left.Put(key, []byte{byte(rng.Intn(256))})
				} else {
					report, err := Reconcile(left, right)
					if err != nil {
						t.Errorf("reconcile: %v", err)
						return
					}
					keys := make([]int, len(report.Diffs))
					for i, d := range report.Diffs {
						keys[i] = d.Key
					}
					sort.Ints(keys)
					for i := 1; i < len(keys); i++ {
						if keys[i] == keys[i-1] {
							t.Errorf("duplicate diff key %d", keys[i])
							return
						}
					}
					for _, c := range report.Sequence {
						if c.Lo < 0 || c.Hi < c.Lo {
							t.Errorf("bad interval")
							return
						}
						if len(c.LeftDigest) == 0 || len(c.RightDigest) == 0 {
							t.Errorf("missing digest in snapshot")
							return
						}
					}
				}
			}
		}(worker)
	}
	// readers that never mutate
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, _, err := left.Get(rand.Intn(size)); err != nil {
					t.Errorf("get: %v", err)
					return
				}
				_ = left.Len()
			}
		}()
	}
	// run a bounded busy loop window
	counter := 0
	for counter < 3000 {
		Reconcile(left, right)
		counter++
	}
	close(stop)
	wg.Wait()
}

func randomData(rng *rand.Rand, size int) map[int][]byte {
	data := map[int][]byte{}
	for _, key := range rng.Perm(size)[:rng.Intn(size+1)] {
		value := make([]byte, rng.Intn(4))
		rng.Read(value)
		if value == nil {
			value = []byte{}
		}
		copied := make([]byte, len(value))
		copy(copied, value)
		data[key] = copied
	}
	return data
}

func cloneData(in map[int][]byte) map[int][]byte {
	out := make(map[int][]byte, len(in))
	for key, value := range in {
		copied := make([]byte, len(value))
		copy(copied, value)
		out[key] = copied
	}
	return out
}
