package compaction

import (
	"fmt"
	"math/big"
	"sort"
)

type candidatePlan struct {
	planType    PlanType
	sourceLayer int
	targetLayer int
	inputs      map[uint64]*File
	minKey      []byte
	maxKey      []byte
}

func (s *Service) CreatePlan() PlanResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	scores := make([]LayerScore, 0, s.config.Layers-1)
	for layer := 0; layer < s.config.Layers-1; layer++ {
		scores = append(scores, LayerScore{Layer: layer, Score: scoreLayer(layer, len(s.layers[layer].files), s.layers[layer].totalBytes, s.config)})
	}
	ranked := rankScores(scores)
	eligible := make([]LayerScore, 0, len(ranked))
	for _, score := range ranked {
		if score.Score.Cmp(big.NewRat(1, 1)) >= 0 {
			eligible = append(eligible, score)
		}
	}

	result := PlanResult{RankedScores: ranked, Skips: map[int]SkipReason{}}
	for _, candidate := range eligible {
		built, err := s.buildCandidate(candidate.Layer)
		if err != nil {
			result.Skips[candidate.Layer] = SkipOccupied
			continue
		}
		plan := s.storeCandidate(built)
		result.Plan = &plan
		result.Reason = fmt.Sprintf("layer %d selected: highest eligible score %s", candidate.Layer, candidate.Score.RatString())
		s.logger.Log("CreatePlan", map[string]any{"rankedScores": ranked, "skips": result.Skips}, result, result.Reason)
		return result
	}
	result.Reason = "no layer has score at least one"
	s.logger.Log("CreatePlan", map[string]any{"rankedScores": ranked}, result, result.Reason)
	return result
}

func (s *Service) buildCandidate(layer int) (*candidatePlan, error) {
	if layer == 0 {
		return s.buildZeroCandidate()
	}
	return s.buildNonZeroCandidate(layer)
}

func (s *Service) buildZeroCandidate() (*candidatePlan, error) {
	layer := &s.layers[0]
	if len(layer.files) == 0 {
		return nil, ErrFileOccupied
	}
	seed := layer.files[0]
	if s.occupied[seed.ID] != 0 {
		return nil, ErrFileOccupied
	}
	selected := map[uint64]*File{seed.ID: seed}
	minKey := append([]byte(nil), seed.MinKey...)
	maxKey := append([]byte(nil), seed.MaxKey...)

	for changed := true; changed; {
		changed = false
		for _, file := range layer.files {
			if selected[file.ID] != nil {
				continue
			}
			if !intervalsOverlap(minKey, maxKey, file.MinKey, file.MaxKey) {
				continue
			}
			if s.occupied[file.ID] != 0 {
				return nil, ErrFileOccupied
			}
			selected[file.ID] = file
			minKey, maxKey = unionBounds(minKey, maxKey, file.MinKey, file.MaxKey)
			changed = true
		}
	}

	return s.addTargetLayer(&candidatePlan{
		planType:    PlanRewrite,
		sourceLayer: 0,
		targetLayer: 1,
		inputs:      selected,
		minKey:      minKey,
		maxKey:      maxKey,
	})
}

func (s *Service) buildNonZeroCandidate(layer int) (*candidatePlan, error) {
	source := &s.layers[layer]
	seedFile := minimumLayerFile(source.files)
	if source.lastEnd != nil {
		if after := firstFileAfter(source.files, source.lastEnd); after != nil {
			seedFile = after
		}
	}
	if seedFile == nil || s.occupied[seedFile.ID] != 0 {
		return nil, ErrFileOccupied
	}

	selected := map[uint64]*File{seedFile.ID: seedFile}
	if err := s.closeNonZeroBoundary(source, selected); err != nil {
		return nil, err
	}
	minKey, maxKey := selectedBounds(selected)

	return s.addTargetLayer(&candidatePlan{
		planType:    PlanRewrite,
		sourceLayer: layer,
		targetLayer: layer + 1,
		inputs:      selected,
		minKey:      minKey,
		maxKey:      maxKey,
	})
}

func minimumLayerFile(files []*File) *File {
	if len(files) == 0 {
		return nil
	}
	result := files[0]
	for _, file := range files[1:] {
		if compareKeys(file.MinKey, result.MinKey) < 0 ||
			(compareKeys(file.MinKey, result.MinKey) == 0 && file.ID < result.ID) {
			result = file
		}
	}
	return result
}

func firstFileAfter(files []*File, key []byte) *File {
	var result *File
	for _, file := range files {
		if compareKeys(file.MinKey, key) <= 0 {
			continue
		}
		if result == nil || compareKeys(file.MinKey, result.MinKey) < 0 ||
			(compareKeys(file.MinKey, result.MinKey) == 0 && file.ID < result.ID) {
			result = file
		}
	}
	return result
}

func (s *Service) closeNonZeroBoundary(layer *layerState, selected map[uint64]*File) error {
	files := append([]*File(nil), layer.files...)
	sort.Slice(files, func(i, j int) bool { return layerFileOrder(files[i], files[j]) < 0 })
	for changed := true; changed; {
		changed = false
		for index, file := range files {
			if selected[file.ID] == nil {
				continue
			}
			neighbors := []*File{}
			if index > 0 {
				neighbors = append(neighbors, files[index-1])
			}
			if index+1 < len(files) {
				neighbors = append(neighbors, files[index+1])
			}
			for _, neighbor := range neighbors {
				if selected[neighbor.ID] != nil {
					continue
				}
				touches := false
				if index > 0 && files[index-1] == neighbor {
					touches = compareKeys(file.MinKey, neighbor.MaxKey) == 0
				}
				if index+1 < len(files) && files[index+1] == neighbor {
					touches = touches || compareKeys(file.MaxKey, neighbor.MinKey) == 0
				}
				if !touches {
					continue
				}
				if s.occupied[neighbor.ID] != 0 {
					return ErrFileOccupied
				}
				selected[neighbor.ID] = neighbor
				changed = true
			}
		}
	}
	return nil
}

func layerFileOrder(left, right *File) int {
	if comparison := compareKeys(left.MinKey, right.MinKey); comparison != 0 {
		return comparison
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func (s *Service) addTargetLayer(candidate *candidatePlan) (*candidatePlan, error) {
	target := &s.layers[candidate.targetLayer]
	targetInputs := map[uint64]*File{}
	for _, file := range target.tree.overlaps(candidate.minKey, candidate.maxKey) {
		if s.occupied[file.ID] != 0 {
			return nil, ErrFileOccupied
		}
		targetInputs[file.ID] = file
	}
	if len(targetInputs) > 0 {
		if err := s.closeNonZeroBoundary(target, targetInputs); err != nil {
			return nil, err
		}
	}
	for id, file := range targetInputs {
		candidate.inputs[id] = file
	}
	if len(candidate.inputs) == 1 && len(targetInputs) == 0 {
		candidate.planType = PlanMoveDown
	}
	candidate.minKey, candidate.maxKey = selectedBounds(candidate.inputs)
	return candidate, nil
}

func (s *Service) storeCandidate(candidate *candidatePlan) Plan {
	id := s.nextPlanID
	s.nextPlanID++
	ids := make([]uint64, 0, len(candidate.inputs))
	for inputID := range candidate.inputs {
		ids = append(ids, inputID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, inputID := range ids {
		s.occupied[inputID] = id
	}
	plan := Plan{
		ID:          id,
		Type:        candidate.planType,
		SourceLayer: candidate.sourceLayer,
		TargetLayer: candidate.targetLayer,
		InputIDs:    ids,
		MinKey:      append([]byte(nil), candidate.minKey...),
		MaxKey:      append([]byte(nil), candidate.maxKey...),
	}
	s.plans[id] = &storedPlan{plan: plan}
	return plan
}

func unionBounds(minLeft, maxLeft, minRight, maxRight []byte) ([]byte, []byte) {
	if compareKeys(minRight, minLeft) < 0 {
		minLeft = append([]byte(nil), minRight...)
	} else {
		minLeft = append([]byte(nil), minLeft...)
	}
	if compareKeys(maxRight, maxLeft) > 0 {
		maxLeft = append([]byte(nil), maxRight...)
	} else {
		maxLeft = append([]byte(nil), maxLeft...)
	}
	return minLeft, maxLeft
}

func selectedBounds(selected map[uint64]*File) ([]byte, []byte) {
	var minKey []byte
	var maxKey []byte
	for _, file := range selected {
		if minKey == nil || compareKeys(file.MinKey, minKey) < 0 {
			minKey = file.MinKey
		}
		if maxKey == nil || compareKeys(file.MaxKey, maxKey) > 0 {
			maxKey = file.MaxKey
		}
	}
	return append([]byte(nil), minKey...), append([]byte(nil), maxKey...)
}
