package compaction

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

type naiveFile struct {
	id     uint64
	layer  int
	minKey []byte
	maxKey []byte
	bytes  uint64
}

type naivePlan struct {
	id     uint64
	typ    PlanType
	source int
	target int
	inputs []uint64
	minKey []byte
	maxKey []byte
}

type naiveModel struct {
	layers   int
	trigger  uint64
	target0  uint64
	factor   uint64
	files    map[uint64]naiveFile
	plans    map[uint64]naivePlan
	occupied map[uint64]uint64
	lastEnds map[int][]byte
	nextPlan uint64
}

func newNaiveModel(layers int, trigger, target0, factor uint64) *naiveModel {
	return &naiveModel{
		layers:   layers,
		trigger:  trigger,
		target0:  target0,
		factor:   factor,
		files:    map[uint64]naiveFile{},
		plans:    map[uint64]naivePlan{},
		occupied: map[uint64]uint64{},
		lastEnds: map[int][]byte{},
		nextPlan: 1,
	}
}

func bytesCompareNaive(left, right []byte) int { return compareKeys(left, right) }
func bytesLessNaive(left, right []byte) bool   { return bytesCompareNaive(left, right) < 0 }
func bytesEqualNaive(left, right []byte) bool  { return bytesCompareNaive(left, right) == 0 }

func inclusiveOverlapNaive(minLeft, maxLeft, minRight, maxRight []byte) bool {
	return compareKeys(minLeft, maxRight) <= 0 && compareKeys(minRight, maxLeft) <= 0
}

func strictOverlapNaive(minLeft, maxLeft, minRight, maxRight []byte) bool {
	return compareKeys(minLeft, maxRight) < 0 && compareKeys(minRight, maxLeft) < 0
}

func containsNaiveFile(files []naiveFile, id uint64) bool {
	return slices.ContainsFunc(files, func(file naiveFile) bool { return file.id == id })
}

func naiveBounds(files []naiveFile) ([]byte, []byte) {
	var minKey []byte
	var maxKey []byte
	for _, file := range files {
		if minKey == nil || compareKeys(file.minKey, minKey) < 0 {
			minKey = file.minKey
		}
		if maxKey == nil || compareKeys(file.maxKey, maxKey) > 0 {
			maxKey = file.maxKey
		}
	}
	return append([]byte(nil), minKey...), append([]byte(nil), maxKey...)
}

func errorID(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			t.Logf("random seed = %d", seed)
			random := rand.New(rand.NewPCG(uint64(seed), uint64(seed*131)))
			service := testService(t, 4, 3, 5, 2)
			model := newNaiveModel(4, 3, 5, 2)
			nextID := uint64(1)
			for operation := 0; operation < 100; operation++ {
				switch random.IntN(6) {
				case 0, 1:
					layer := random.IntN(4)
					left := byte('a' + random.IntN(4))
					right := byte(left) + byte(random.IntN(3))
					file := File{ID: nextID, Layer: layer, MinKey: []byte{left}, MaxKey: []byte{right}, Bytes: 1 + uint64(random.IntN(6))}
					got := service.RegisterFile(file)
					want := model.register(naiveFile{file.ID, file.Layer, file.MinKey, file.MaxKey, file.Bytes})
					t.Logf("op=%d action=RegisterFile input=%+v output=%v decision=existing-layer-invariant-check", operation, file, errorID(want))
					if errorID(got) != errorID(want) {
						t.Fatalf("register mismatch: got=%v want=%v", got, want)
					}
					if got == nil {
						nextID++
					}
				case 2:
					got := service.CreatePlan()
					want := model.create()
					t.Logf("op=%d action=CreatePlan input={} output=%+v decision=exact-score-order-occupancy-boundary-closure", operation, want)
					if (got.Plan == nil) != (want == nil) {
						t.Fatalf("plan presence mismatch: got=%+v want=%+v", got.Plan, want)
					}
					if want != nil {
						if got.Plan.ID != want.id || got.Plan.Type != want.typ || got.Plan.SourceLayer != want.source || got.Plan.TargetLayer != want.target ||
							!slices.Equal(got.Plan.InputIDs, want.inputs) || !slices.Equal(got.Plan.MinKey, want.minKey) || !slices.Equal(got.Plan.MaxKey, want.maxKey) {
							t.Fatalf("plan mismatch: got=%+v want=%+v", got.Plan, want)
						}
					}
				case 3:
					if len(model.plans) == 0 {
						continue
					}
					id := choosePlanID(random, model.plans)
					plan := model.plans[id]
					outputs := randomOutputs(random, plan, model.files)
					got := service.InstallPlan(id, outputs)
					want := model.install(id, toNaiveOutputs(outputs))
					t.Logf("op=%d action=InstallPlan input={plan:%d outputs:%+v} output=%v decision=validate-then-atomic-commit", operation, id, outputs, errorID(want))
					if errorID(got) != errorID(want) {
						t.Fatalf("install mismatch: got=%v want=%v", got, want)
					}
				case 4:
					if len(model.plans) == 0 {
						continue
					}
					id := choosePlanID(random, model.plans)
					got := service.CancelPlan(id)
					want := model.cancel(id)
					t.Logf("op=%d action=CancelPlan input=%d output=%v decision=release-occupancy-keep-last-end", operation, id, errorID(want))
					if errorID(got) != errorID(want) {
						t.Fatalf("cancel mismatch: got=%v want=%v", got, want)
					}
				default:
					if random.IntN(2) == 0 {
						continue
					}
					if got := service.InstallPlan(999, nil); !errors.Is(got, ErrInvalidArgument) {
						t.Fatalf("invalid operation error = %v", got)
					}
					if got := model.install(999, nil); !errors.Is(got, ErrInvalidArgument) {
						t.Fatalf("naive invalid operation error = %v", got)
					}
				}
				if diff := compareSnapshots(service.Snapshot(), model); diff != "" {
					t.Fatalf("state mismatch at op %d: %s", operation, diff)
				}
			}
		})
	}
}

func choosePlanID(random *rand.Rand, plans map[uint64]naivePlan) uint64 {
	ids := make([]uint64, 0, len(plans))
	for id := range plans {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids[random.IntN(len(ids))]
}

func randomOutputs(random *rand.Rand, plan naivePlan, files map[uint64]naiveFile) []File {
	if plan.typ == PlanMoveDown {
		input := files[plan.inputs[0]]
		id := input.id
		if random.IntN(3) == 0 {
			id = 900000 + uint64(random.IntN(10000))
		}
		return []File{{ID: id, Layer: plan.target, MinKey: append([]byte(nil), plan.minKey...), MaxKey: append([]byte(nil), plan.maxKey...), Bytes: input.bytes}}
	}
	outputs := []File{{ID: 800000 + uint64(random.IntN(10000)), Layer: plan.target, MinKey: append([]byte(nil), plan.minKey...), MaxKey: append([]byte(nil), plan.maxKey...), Bytes: 1}}
	if random.IntN(3) == 0 {
		outputs[0].MinKey = []byte{byte('a' + random.IntN(5))}
		outputs[0].MaxKey = []byte{byte('a' + random.IntN(5))}
	}
	return outputs
}

func toNaiveOutputs(outputs []File) []naiveFile {
	result := make([]naiveFile, 0, len(outputs))
	for _, output := range outputs {
		result = append(result, naiveFile{output.ID, output.Layer, output.MinKey, output.MaxKey, output.Bytes})
	}
	return result
}

func compareSnapshots(snapshot Snapshot, model *naiveModel) string {
	if len(snapshot.Files) != len(model.files) {
		return fmt.Sprintf("file count %d != %d", len(snapshot.Files), len(model.files))
	}
	for id, got := range snapshot.Files {
		want, exists := model.files[id]
		if !exists || got.Layer != want.layer || got.Bytes != want.bytes || !slices.Equal(got.MinKey, want.minKey) || !slices.Equal(got.MaxKey, want.maxKey) {
			return fmt.Sprintf("file %d mismatch: got=%+v want=%+v", id, got, want)
		}
	}
	if len(snapshot.Plans) != len(model.plans) {
		return fmt.Sprintf("plan count %d != %d", len(snapshot.Plans), len(model.plans))
	}
	for id, got := range snapshot.Plans {
		want, exists := model.plans[id]
		if !exists || got.Type != want.typ || got.SourceLayer != want.source || got.TargetLayer != want.target || !slices.Equal(got.InputIDs, want.inputs) || !slices.Equal(got.MinKey, want.minKey) || !slices.Equal(got.MaxKey, want.maxKey) {
			return fmt.Sprintf("plan %d mismatch: got=%+v want=%+v", id, got, want)
		}
	}
	if len(snapshot.LastEnds) != len(model.lastEnds) {
		return fmt.Sprintf("lastEnd count %d != %d", len(snapshot.LastEnds), len(model.lastEnds))
	}
	for layer, got := range snapshot.LastEnds {
		if !slices.Equal(got, model.lastEnds[layer]) {
			return fmt.Sprintf("lastEnd %d mismatch: %q != %q", layer, got, model.lastEnds[layer])
		}
	}
	if len(snapshot.Occupied) != len(model.occupied) {
		return fmt.Sprintf("occupied count %d != %d", len(snapshot.Occupied), len(model.occupied))
	}
	for id, got := range snapshot.Occupied {
		if got != model.occupied[id] {
			return fmt.Sprintf("occupied %d mismatch: %d != %d", id, got, model.occupied[id])
		}
	}
	return ""
}

func (model *naiveModel) register(file naiveFile) error {
	if file.layer < 0 || file.layer >= model.layers || file.minKey == nil || file.maxKey == nil || bytesLessNaive(file.maxKey, file.minKey) || file.id == 0 {
		return ErrInvalidArgument
	}
	if _, exists := model.files[file.id]; exists {
		return ErrInvalidArgument
	}
	for _, existing := range model.files {
		if existing.layer == file.layer && file.layer > 0 && strictOverlapNaive(file.minKey, file.maxKey, existing.minKey, existing.maxKey) {
			return ErrLayerInvariant
		}
	}
	model.files[file.id] = file
	return nil
}

func (model *naiveModel) layerFiles(layer int) []naiveFile {
	var files []naiveFile
	for _, file := range model.files {
		if file.layer == layer {
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(left, right naiveFile) int {
		if cmp := bytesCompareNaive(left.minKey, right.minKey); cmp != 0 {
			return cmp
		}
		return int(left.id) - int(right.id)
	})
	return files
}

func (model *naiveModel) create() *naivePlan {
	type score struct {
		layer int
		num   uint64
		den   uint64
	}
	var scores []score
	for layer := 0; layer < model.layers-1; layer++ {
		files := model.layerFiles(layer)
		var total uint64
		for _, file := range files {
			total += file.bytes
		}
		if layer == 0 {
			scores = append(scores, score{layer, uint64(len(files)), model.trigger})
			continue
		}
		denominator := model.target0
		for index := 1; index < layer; index++ {
			denominator *= model.factor
		}
		scores = append(scores, score{layer, total, denominator})
	}
	slices.SortFunc(scores, func(left, right score) int {
		if left.num*right.den != right.num*left.den {
			if left.num*right.den > right.num*left.den {
				return -1
			}
			return 1
		}
		return left.layer - right.layer
	})
	for _, candidate := range scores {
		if candidate.num < candidate.den {
			continue
		}
		if plan, ok := model.build(candidate.layer); ok {
			return plan
		}
	}
	return nil
}

func (model *naiveModel) build(layer int) (*naivePlan, bool) {
	var inputs []naiveFile
	if layer == 0 {
		files := model.layerFiles(0)
		slices.SortFunc(files, func(left, right naiveFile) int { return int(left.id) - int(right.id) })
		if len(files) == 0 || model.occupied[files[0].id] != 0 {
			return nil, false
		}
		inputs = []naiveFile{files[0]}
		for changed := true; changed; {
			changed = false
			minKey, maxKey := naiveBounds(inputs)
			for _, file := range files {
				if containsNaiveFile(inputs, file.id) || !inclusiveOverlapNaive(minKey, maxKey, file.minKey, file.maxKey) {
					continue
				}
				if model.occupied[file.id] != 0 {
					return nil, false
				}
				inputs = append(inputs, file)
				changed = true
			}
		}
	} else {
		files := model.layerFiles(layer)
		var seed *naiveFile
		if end, exists := model.lastEnds[layer]; exists {
			for index := range files {
				if bytesCompareNaive(files[index].minKey, end) > 0 {
					seed = &files[index]
					break
				}
			}
			if seed == nil && len(files) > 0 {
				seed = &files[0]
			}
		} else if len(files) > 0 {
			seed = &files[0]
		}
		if seed == nil || model.occupied[seed.id] != 0 {
			return nil, false
		}
		inputs = []naiveFile{*seed}
		if !model.close(layer, &inputs) {
			return nil, false
		}
	}

	target := layer + 1
	minKey, maxKey := naiveBounds(inputs)
	var targetInputs []naiveFile
	for _, file := range model.layerFiles(target) {
		if inclusiveOverlapNaive(minKey, maxKey, file.minKey, file.maxKey) {
			if model.occupied[file.id] != 0 {
				return nil, false
			}
			targetInputs = append(targetInputs, file)
		}
	}
	if !model.close(target, &targetInputs) {
		return nil, false
	}
	inputs = append(inputs, targetInputs...)
	minKey, maxKey = naiveBounds(inputs)
	typ := PlanRewrite
	if len(inputs) == 1 && len(targetInputs) == 0 {
		typ = PlanMoveDown
	}
	plan := &naivePlan{id: model.nextPlan, typ: typ, source: layer, target: target, minKey: minKey, maxKey: maxKey}
	for _, file := range inputs {
		plan.inputs = append(plan.inputs, file.id)
	}
	slices.Sort(plan.inputs)
	model.plans[plan.id] = *plan
	for _, id := range plan.inputs {
		model.occupied[id] = plan.id
	}
	model.nextPlan++
	return plan, true
}

func (model *naiveModel) close(layer int, selected *[]naiveFile) bool {
	for changed := true; changed; {
		changed = false
		files := model.layerFiles(layer)
		currentSelected := append([]naiveFile(nil), *selected...)
		for _, current := range currentSelected {
			currentIndex := -1
			for index, file := range files {
				if file.id == current.id {
					currentIndex = index
					break
				}
			}
			if currentIndex < 0 {
				continue
			}
			for _, neighborIndex := range []int{currentIndex - 1, currentIndex + 1} {
				if neighborIndex < 0 || neighborIndex >= len(files) {
					continue
				}
				file := files[neighborIndex]
				if containsNaiveFile(*selected, file.id) {
					continue
				}
				touches := false
				if neighborIndex == currentIndex-1 {
					touches = bytesEqualNaive(current.minKey, file.maxKey)
				} else {
					touches = bytesEqualNaive(current.maxKey, file.minKey)
				}
				if !touches {
					continue
				}
				if model.occupied[file.id] != 0 {
					return false
				}
				*selected = append(*selected, file)
				changed = true
			}
		}
	}
	return true
}

func (model *naiveModel) install(id uint64, outputs []naiveFile) error {
	if id == 0 || len(outputs) == 0 {
		return ErrInvalidArgument
	}
	seen := map[uint64]bool{}
	for _, output := range outputs {
		if output.layer < 0 || output.layer >= model.layers || output.id == 0 || output.minKey == nil || output.maxKey == nil || bytesLessNaive(output.maxKey, output.minKey) || seen[output.id] {
			return ErrInvalidArgument
		}
		seen[output.id] = true
	}
	plan, planExists := model.plans[id]
	if !planExists {
		for _, output := range outputs {
			if model.occupied[output.id] != 0 {
				return ErrFileOccupied
			}
		}
		return ErrPlanNotFound
	}
	inputSet := map[uint64]bool{}
	for _, input := range plan.inputs {
		inputSet[input] = true
	}
	for _, output := range outputs {
		if output.layer != plan.target {
			return ErrInvalidArgument
		}
		if _, exists := model.files[output.id]; exists && !inputSet[output.id] {
			return ErrInvalidArgument
		}
	}
	if plan.typ == PlanMoveDown {
		intervalMatches := bytesEqualNaive(outputs[0].minKey, plan.minKey) && bytesEqualNaive(outputs[0].maxKey, plan.maxKey)
		input, exists := model.files[plan.inputs[0]]
		sizeMatches := !exists || outputs[0].bytes == input.bytes
		if len(outputs) != 1 || !intervalMatches || !sizeMatches {
			return ErrInvalidArgument
		}
	}
	for _, output := range outputs {
		if model.occupied[output.id] != 0 && !inputSet[output.id] {
			return ErrFileOccupied
		}
	}
	for _, input := range plan.inputs {
		if _, exists := model.files[input]; !exists {
			return ErrFileNotFound
		}
	}
	for _, output := range outputs {
		for _, existing := range model.files {
			if existing.layer == output.layer && !inputSet[existing.id] && strictOverlapNaive(output.minKey, output.maxKey, existing.minKey, existing.maxKey) {
				return ErrLayerInvariant
			}
		}
	}
	for i := range outputs {
		for j := i + 1; j < len(outputs); j++ {
			if strictOverlapNaive(outputs[i].minKey, outputs[i].maxKey, outputs[j].minKey, outputs[j].maxKey) {
				return ErrLayerInvariant
			}
		}
	}

	var installedMax []byte
	for _, input := range plan.inputs {
		file := model.files[input]
		if installedMax == nil || bytesCompareNaive(file.maxKey, installedMax) > 0 {
			installedMax = file.maxKey
		}
		delete(model.files, input)
	}
	for _, output := range outputs {
		model.files[output.id] = output
	}
	model.lastEnds[plan.source] = append([]byte(nil), installedMax...)
	for _, input := range plan.inputs {
		delete(model.occupied, input)
	}
	delete(model.plans, id)
	return nil
}

func (model *naiveModel) cancel(id uint64) error {
	if id == 0 {
		return ErrInvalidArgument
	}
	plan, exists := model.plans[id]
	if !exists {
		return ErrPlanNotFound
	}
	for _, input := range plan.inputs {
		delete(model.occupied, input)
	}
	delete(model.plans, id)
	return nil
}
