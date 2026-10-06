package imaging

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// Op 是可同时施加到主实现与朴素模型的一步操作，并能复现输入描述。
type Op interface {
	ApplyMain(h *Hospital) error
	ApplyNaive(n *NaiveModel) error
	String() string
	Reason() string // 判定依据（与具体实现无关的业务解释）
}

type opRegDevice struct{ req RegisterDeviceRequest }

func (o opRegDevice) ApplyMain(h *Hospital) error    { return h.RegisterDevice(o.req) }
func (o opRegDevice) ApplyNaive(n *NaiveModel) error { return n.RegisterDevice(o.req) }

func runDiff(t *testing.T, seed, ops int) {
	t.Helper()
	h, err := NewHospital(diffConfig())
	if err != nil {
		t.Fatal(err)
	}
	nm := NewNaiveModel(diffConfig())
	rng := rand.New(rand.NewSource(int64(seed)))
	f := &fuzzState{rng: rng}
	for i := 0; i < ops; i++ {
		op := f.gen()
		e1 := op.ApplyMain(h)
		e2 := op.ApplyNaive(nm)
		if fmt.Sprint(e1) != fmt.Sprint(e2) {
			t.Fatalf("seed=%d step=%d\n输入: %s\n判定依据: %s\n主实现=%v\n朴素=%v", seed, i, op, op.Reason(), e1, e2)
		}
		s1, s2 := h.Snapshot(), nm.Snapshot()
		if !reflect.DeepEqual(s1, s2) {
			t.Fatalf("seed=%d step=%d\n输入: %s\n状态不一致\n主=%#v\n朴素=%#v", seed, i, op, s1, s2)
		}
		t.Logf("step=%d | %s | => %v | %s", i, op, e1, op.Reason())
	}
}

func diffConfig() Config {
	return Config{
		ValidityNormal: 300, ValidityHighRisk: 120,
		KidneyLow: 30, KidneyHigh: 60,
		HydrationLead: 100, PremedicationLead: 150,
		ObservationMinutes: 40, ObservationCapacity: 2,
		CleaningMinutes: map[DeviceClass]int{ClassCT: 20, ClassMR: 25},
	}
}

type fuzzState struct {
	rng      *rand.Rand
	devices  []string
	ctDevs   []string
	mrDevs   []string
	exams    []examSpec
	patients []patientSpec
	appts    []string
	step     int
}

type examSpec struct {
	id       string
	class    DeviceClass
	duration int
	enhanced bool
}

type patientSpec struct {
	id        string
	highRisk  bool
	noImplant bool
	maxFS     int
	allergic  bool
}
