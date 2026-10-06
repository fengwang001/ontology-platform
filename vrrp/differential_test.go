package vrrp_test

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/vrrp"
)

func runStep(device *vrrp.Device, current step) (vrrp.EventResult, error) {
	switch current.kind {
	case opStart:
		return device.Start(current.now)
	case opStop:
		return device.Stop(current.now)
	case opReceive:
		return device.ReceiveAdvertisement(vrrp.Advertisement{
			SenderID:         current.sender,
			Priority:         current.priority,
			AdvertIntervalMS: current.interval,
		}, current.now)
	case opAdvance:
		return device.AdvanceTime(current.now)
	case opTake:
		return device.TakeAdvertisements(current.now)
	case opSetPriority:
		return device.SetPriority(current.priority, current.now)
	case opSetPreempt:
		return device.SetPreempt(current.preempt, current.now)
	default:
		return vrrp.EventResult{}, vrrp.ErrInvalidArgument
	}
}

func generateStep(r *rand.Rand, m *naiveModel) step {
	now := m.lastTime
	if m.hasClock && now > 0 && r.Intn(12) == 0 {
		now--
	} else if !m.hasClock {
		now = uint64(r.Intn(5))
	} else {
		now += uint64(r.Intn(21))
	}

	current := step{now: now}
	kind := operationKind(r.Intn(int(opSetPreempt) + 1))
	current.kind = kind

	switch kind {
	case opReceive:
		current.sender = []string{"", "a", "b", "c"}[r.Intn(4)]
		current.interval = []uint64{0, 1, 4, 5, 10, 100}[r.Intn(6)]
		priorities := []int{
			0, 1, 50, 100, 150, 254, 255,
			m.priority,
			maxInt(1, m.priority-1),
			minInt(255, m.priority+1),
		}
		current.priority = priorities[r.Intn(len(priorities))]
	case opSetPriority:
		candidates := []int{0, 1, 2, 50, 100, 200, 254, 255}
		current.priority = candidates[r.Intn(len(candidates))]
		if m.priority == 255 {
			current.priority = []int{0, 254, 255}[r.Intn(3)]
		}
	case opSetPreempt:
		current.preempt = r.Intn(2) == 0
	}

	return current
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestDifferentialRandomSequences(t *testing.T) {
	const sequenceCount = 1000
	const stepsPerSequence = 24

	for sequence := 0; sequence < sequenceCount; sequence++ {
		t.Run("sequence", func(t *testing.T) {
			r := rand.New(rand.NewSource(int64(sequence + 1)))
			id := []string{"a", "b", "c"}[r.Intn(3)]
			priority := 1 + r.Intn(254)
			if r.Intn(8) == 0 {
				priority = 255
			}
			config := vrrp.Config{
				ID:               id,
				Priority:         priority,
				Preempt:          r.Intn(2) == 0,
				AdvertIntervalMS: []uint64{1, 4, 5, 10, 100}[r.Intn(5)],
			}

			device, err := vrrp.NewDevice(config)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveModel(config)

			for index := 0; index < stepsPerSequence; index++ {
				current := generateStep(r, model)
				got, gotErr := runStep(device, current)
				want := model.handle(current)

				t.Logf(
					"seq=%d step=%d local={id=%q initialPriority=%d interval=%d} input={kind=%s now=%d sender=%q priority=%d interval=%d preempt=%t} output={role=%s adverts=%v err=%v} model={role=%s adverts=%v err=%v} decision=%q",
					sequence+1, index+1, config.ID, config.Priority, config.AdvertIntervalMS,
					operationName(current.kind), current.now, current.sender,
					current.priority, current.interval, current.preempt,
					roleName(device.Role()), got.Advertisements, gotErr,
					roleName(want.role), want.adverts, want.err, want.reason,
				)

				if !sameError(gotErr, want.err) {
					t.Fatalf("error = %v, want %v", gotErr, want.err)
				}
				if device.Role() != want.role ||
					device.Priority() != want.priority ||
					device.Preempt() != want.preempt ||
					!reflect.DeepEqual(got.Advertisements, want.adverts) {
					t.Fatalf("state/output mismatch: got role=%s priority=%d preempt=%t adverts=%#v; want role=%s priority=%d preempt=%t adverts=%#v",
						roleName(device.Role()), device.Priority(), device.Preempt(), got.Advertisements,
						roleName(want.role), want.priority, want.preempt, want.adverts)
				}
			}
		})
	}
}

func operationName(kind operationKind) string {
	switch kind {
	case opStart:
		return "start"
	case opStop:
		return "stop"
	case opReceive:
		return "receive"
	case opAdvance:
		return "advance"
	case opTake:
		return "take"
	case opSetPriority:
		return "set-priority"
	case opSetPreempt:
		return "set-preempt"
	default:
		return "unknown"
	}
}

func roleName(role vrrp.Role) string {
	switch role {
	case vrrp.RoleInitialize:
		return "initialize"
	case vrrp.RoleBackup:
		return "backup"
	case vrrp.RoleMaster:
		return "master"
	default:
		return "invalid"
	}
}
