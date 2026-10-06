package pvbinding

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestRandomDifferentialAgainstNaiveOracle(t *testing.T) {
	for _, seed := range []int64{1, 17, 1616, 2026, 9181} {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runRandomDifferential(t, seed)
		})
	}
}

func runRandomDifferential(t *testing.T, seed int64) {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	controller := NewController()
	reference := newOracle()
	log := newOperationLog(t)
	log.record(fmt.Sprintf("Seed(%d)", seed), nil, "初始化生产控制器与独立朴素模型")

	classes := []string{"c0", "c1"}
	modeChoices := [][]string{{"RWO"}, {"ROX"}, {"RWO", "ROX"}}
	nodes := []string{"n0", "n1"}
	volumeCounter := 0
	claimCounter := 0

	compare := func(input string, err error, expected ErrorCode) {
		t.Helper()
		actualCode := errorCode(err)
		log.record(input, err, fmt.Sprintf("参考模型=%d，实现=%d；随后比较全量状态", expected, actualCode))
		if actualCode != expected {
			t.Fatalf("%s: expected %d, got %d (%v)", input, expected, actualCode, err)
		}
		if err := controller.CheckConsistency(); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		compareOracleState(t, controller, reference)
	}

	for operation := 0; operation < 300; operation++ {
		switch random.Intn(10) {
		case 0, 1, 2:
			volumeCounter++
			spec := VolumeSpec{
				Name:          fmt.Sprintf("pv-%03d", volumeCounter),
				Capacity:      int64(random.Intn(5) + 1),
				StorageClass:  classes[random.Intn(len(classes))],
				AccessModes:   append([]string(nil), modeChoices[random.Intn(len(modeChoices))]...),
				Labels:        map[string]string{"k": fmt.Sprintf("v%d", random.Intn(2))},
				ReclaimPolicy: []ReclaimPolicy{ReclaimRetain, ReclaimDelete}[random.Intn(2)],
			}
			if random.Intn(2) == 0 {
				spec.NodeNames = []string{nodes[random.Intn(len(nodes))]}
			}
			err := controller.CreateVolume(spec)
			compare("CreateVolume "+describe(spec), err, reference.createVolume(spec))
		case 3, 4, 5:
			claimCounter++
			spec := ClaimSpec{
				Name:              fmt.Sprintf("pvc-%03d", claimCounter),
				RequestedCapacity: int64(random.Intn(4) + 1),
				StorageClass:      classes[random.Intn(len(classes))],
				AccessModes:       []string{modeChoices[random.Intn(len(modeChoices))][0]},
				Selector:          map[string]string{},
				BindingMode:       []BindingMode{BindingImmediate, BindingDelayed}[random.Intn(2)],
			}
			if random.Intn(2) == 0 {
				spec.Selector["k"] = fmt.Sprintf("v%d", random.Intn(2))
			}
			err := controller.CreateClaim(spec)
			compare("CreateClaim "+describe(spec), err, reference.createClaim(spec))
		case 6:
			names := oracleDelayedNames(reference)
			if len(names) == 0 {
				continue
			}
			random.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
			limit := len(names)
			if limit > 3 {
				limit = 3
			}
			chosen := names[:random.Intn(limit)+1]
			node := nodes[random.Intn(len(nodes))]
			err := controller.BindDelayed(node, chosen)
			compare(fmt.Sprintf("BindDelayed node=%s claims=%v", node, chosen), err, reference.bindDelayed(node, chosen))
		case 7:
			name := oracleRandomClaim(reference, random)
			if name == "" {
				continue
			}
			err := controller.DeleteClaim(name)
			compare("DeleteClaim "+name, err, reference.deleteClaim(name))
		case 8:
			name, capacity := oracleRandomExpansion(reference, random)
			if name == "" {
				continue
			}
			err := controller.ExpandClaim(name, capacity)
			compare(fmt.Sprintf("ExpandClaim %s -> %d", name, capacity), err, reference.expand(name, capacity))
		case 9:
			name := oracleRandomVolume(reference, random)
			if name == "" {
				continue
			}
			spec := reference.volumes[name].spec
			spec.Capacity = int64(random.Intn(6) + 1)
			err := controller.UpdateVolume(spec)
			compare("UpdateVolume "+describe(spec), err, reference.updateVolume(spec))
		}
	}
}

func oracleDelayedNames(o *oracle) []string {
	names := make([]string, 0)
	for name, claim := range o.claims {
		if claim.spec.BindingMode == BindingDelayed && claim.boundVolume == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func oracleRandomClaim(o *oracle, random *rand.Rand) string {
	names := make([]string, 0, len(o.claims))
	for name := range o.claims {
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	return names[random.Intn(len(names))]
}

func oracleRandomVolume(o *oracle, random *rand.Rand) string {
	names := make([]string, 0, len(o.volumes))
	for name := range o.volumes {
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	return names[random.Intn(len(names))]
}

func oracleRandomExpansion(o *oracle, random *rand.Rand) (string, int64) {
	name := oracleRandomClaim(o, random)
	if name == "" {
		return "", 0
	}
	claim := o.claims[name]
	if claim.boundVolume == "" {
		return name, claim.spec.RequestedCapacity
	}
	volumeCapacity := o.volumes[claim.boundVolume].spec.Capacity
	return name, claim.spec.RequestedCapacity + int64(random.Intn(int(volumeCapacity)+2))
}

func compareOracleState(t *testing.T, controller *Controller, reference *oracle) {
	t.Helper()
	for name, expectedVolume := range reference.volumes {
		actual, ok := controller.GetVolume(name)
		if !ok {
			t.Fatalf("volume %s missing", name)
		}
		if actual.State != expectedVolume.state ||
			actual.Capacity != expectedVolume.spec.Capacity ||
			actual.StorageClass != expectedVolume.spec.StorageClass ||
			actual.ReclaimPolicy != expectedVolume.spec.ReclaimPolicy {
			t.Fatalf("volume %s = %+v, want state %s spec %+v", name, actual, expectedVolume.state, expectedVolume.spec)
		}
	}
	for name, expectedClaim := range reference.claims {
		actual, ok := controller.GetClaim(name)
		if !ok {
			t.Fatalf("claim %s missing", name)
		}
		if actual.BoundVolumeName != expectedClaim.boundVolume ||
			actual.RequestedCapacity != expectedClaim.spec.RequestedCapacity {
			t.Fatalf("claim %s = %+v, want volume %s spec %+v", name, actual, expectedClaim.boundVolume, expectedClaim.spec)
		}
	}
}
