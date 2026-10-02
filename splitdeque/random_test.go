package splitdeque

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naiveSplitDeque struct {
	capacity  int
	sharedMax int
	reserve   int
	freshness int

	data   []int64
	top    int64
	split  int64
	bottom int64

	requested bool
	age       int64
	missing   int64

	stats          Stats
	movedElements  int64
	copiedElements int64
}

type randomOp struct {
	kind  string
	value int64
}

func newNaive(capacity, sharedLimit, privateReserve, freshness int) *naiveSplitDeque {
	return &naiveSplitDeque{
		capacity:  capacity,
		sharedMax: sharedLimit,
		reserve:   privateReserve,
		freshness: freshness,
		data:      make([]int64, capacity),
	}
}

func (n *naiveSplitDeque) push(value int64) error {
	if n.bottom-n.top == int64(n.capacity) {
		return ErrFull
	}

	n.data[n.bottom%int64(n.capacity)] = value
	n.bottom++
	n.stats.Pushed++
	n.release()
	return nil
}

func (n *naiveSplitDeque) pop() (int64, bool) {
	n.release()

	if n.bottom > n.split {
		n.bottom--
		value := n.data[n.bottom%int64(n.capacity)]
		n.stats.Popped++
		return value, true
	}

	if n.split == n.top {
		return 0, false
	}

	giveBack := (n.split - n.top + 1) / 2
	n.split -= giveBack
	n.stats.Reclaims++

	n.bottom--
	value := n.data[n.bottom%int64(n.capacity)]
	n.stats.Popped++
	return value, true
}

func (n *naiveSplitDeque) steal(count int) ([]int64, error) {
	if count < 1 || count > n.capacity {
		return nil, ErrInvalidArgument
	}

	shared := n.split - n.top
	taken := int64(count)
	if taken > shared {
		taken = shared
	}

	values := make([]int64, 0, taken)
	for index := n.top; index < n.top+taken; index++ {
		values = append(values, n.data[index%int64(n.capacity)])
		n.copiedElements++
	}
	n.top += taken
	n.stats.Stolen += taken

	if taken < int64(count) {
		missing := int64(count) - taken
		if !n.requested || missing > n.missing {
			n.missing = missing
		}
		n.requested = true
		n.age = 0
		n.stats.FailedSteals++
	}

	return values, nil
}

func (n *naiveSplitDeque) release() {
	if !n.requested {
		return
	}

	privateCount := n.bottom - n.split
	sharedCount := n.split - n.top
	desired := privateCount / 2
	if n.missing > desired {
		desired = n.missing
	}

	releaseCount := desired
	if room := int64(n.sharedMax) - sharedCount; room < releaseCount {
		releaseCount = room
	}
	if surplus := privateCount - int64(n.reserve); surplus < releaseCount {
		releaseCount = surplus
	}
	if releaseCount < 0 {
		releaseCount = 0
	}

	if releaseCount >= 1 {
		n.split += releaseCount
		n.requested = false
		n.age = 0
		n.missing = 0
		n.stats.Releases++
		return
	}

	n.age++
	if n.age == int64(n.freshness) {
		n.requested = false
		n.age = 0
		n.missing = 0
	}
}

func actualSnapshot(d *SplitDeque) []int64 {
	return []int64{d.top, d.split, d.bottom, d.age, d.missing, d.movedElements, d.copiedElements}
}

func naiveSnapshot(n *naiveSplitDeque) []int64 {
	return []int64{n.top, n.split, n.bottom, n.age, n.missing, n.movedElements, n.copiedElements}
}

func compareDeques(t *testing.T, seed int64, step int, d *SplitDeque, n *naiveSplitDeque, reason string) {
	t.Helper()

	actualState := actualSnapshot(d)
	modelState := naiveSnapshot(n)
	actualRequested := d.requested
	modelRequested := n.requested
	actualStats := d.Stats()
	modelStats := n.stats

	t.Logf("判定依据: %s", reason)
	t.Logf("实际状态: t/s/b/ag/dm/moved/copied=%v fl=%t stats=%+v", actualState, actualRequested, actualStats)
	t.Logf("模型状态: t/s/b/ag/dm/moved/copied=%v fl=%t stats=%+v", modelState, modelRequested, modelStats)

	if !reflect.DeepEqual(actualState, modelState) || actualRequested != modelRequested || actualStats != modelStats {
		t.Fatalf("seed=%d step=%d mismatch", seed, step)
	}
	if !(d.top <= d.split && d.split <= d.bottom) {
		t.Fatalf("seed=%d split invariant violated: t=%d s=%d b=%d", seed, d.top, d.split, d.bottom)
	}
	if d.split-d.top > int64(d.sharedMax) {
		t.Fatalf("seed=%d shared limit violated: |S|=%d Sm=%d", seed, d.split-d.top, d.sharedMax)
	}
	if actualStats.Pushed != actualStats.Popped+actualStats.Stolen+d.bottom-d.top {
		t.Fatalf("seed=%d accounting mismatch", seed)
	}
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1214))

	for trial := 0; trial < 2000; trial++ {
		seed := rng.Int63()
		trialRNG := rand.New(rand.NewSource(seed))
		capacity := 1 + trialRNG.Intn(12)
		sharedLimit := 1 + trialRNG.Intn(capacity)
		privateReserve := trialRNG.Intn(capacity + 1)
		freshness := 1 + trialRNG.Intn(4)

		actual, err := New(capacity, sharedLimit, privateReserve, freshness)
		if err != nil {
			t.Fatalf("trial %d New: %v", trial, err)
		}
		model := newNaive(capacity, sharedLimit, privateReserve, freshness)

		operationCount := 30 + trialRNG.Intn(50)
		t.Logf("trial=%d seed=%d 输入: Cap=%d Sm=%d Rv=%d F=%d operations=%d",
			trial, seed, capacity, sharedLimit, privateReserve, freshness, operationCount)

		for step := 0; step < operationCount; step++ {
			op := randomOp{kind: "push"}
			switch trialRNG.Intn(10) {
			case 0, 1, 2, 3:
				op.kind = "push"
				op.value = trialRNG.Int63()
			case 4, 5, 6:
				op.kind = "pop"
			case 7, 8:
				op.kind = "steal"
				op.value = int64(1 + trialRNG.Intn(capacity))
			case 9:
				op.kind = "steal"
				if trialRNG.Intn(2) == 0 {
					op.value = 0
				} else {
					op.value = int64(capacity + 1)
				}
			}

			switch op.kind {
			case "push":
				actualErr := actual.Push(op.value)
				modelErr := model.push(op.value)
				t.Logf("step=%d 输入=Push(%d) 输出=(%v) 判定=拒绝规则/追加后释放检查/满员不变量", step, op.value, actualErr)
				if !errors.Is(actualErr, modelErr) {
					t.Fatalf("seed=%d Push error actual=%v model=%v", seed, actualErr, modelErr)
				}
			case "pop":
				actualValue, actualOK := actual.Pop()
				modelValue, modelOK := model.pop()
				t.Logf("step=%d 输入=Pop 输出=(%d,%t) 判定=释放检查先于取元素/回收⌈|S|/2⌉", step, actualValue, actualOK)
				if actualValue != modelValue || actualOK != modelOK {
					t.Fatalf("seed=%d Pop actual=(%d,%t) model=(%d,%t)", seed, actualValue, actualOK, modelValue, modelOK)
				}
			case "steal":
				actualValues, actualErr := actual.Steal(int(op.value))
				modelValues, modelErr := model.steal(int(op.value))
				t.Logf("step=%d 输入=Steal(%d) 输出=(%v,%v) 判定=只取共享区FIFO/缺口与失败请求规则", step, op.value, actualValues, actualErr)
				if !reflect.DeepEqual(actualValues, modelValues) || !errors.Is(actualErr, modelErr) {
					t.Fatalf("seed=%d Steal actual=(%v,%v) model=(%v,%v)", seed, actualValues, actualErr, modelValues, modelErr)
				}
			}

			compareDeques(t, seed, step, actual, model, fmt.Sprintf("trial=%d step=%d 完整状态、fl、ag、dm、统计、不变量", trial, step))
		}
	}
}
