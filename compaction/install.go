package compaction

import (
	"math/big"
	"sort"
)

func (s *Service) InstallPlan(planID uint64, outputs []File) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if planID == 0 || len(outputs) == 0 {
		return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "plan id and non-empty outputs are required")
	}
	seenOutputs := map[uint64]bool{}
	for _, output := range outputs {
		if err := validateFile(output, s.config); err != nil || output.ID == 0 {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "output layer or key range is invalid")
		}
		if seenOutputs[output.ID] {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "output ids must be unique")
		}
		seenOutputs[output.ID] = true
	}

	stored := s.plans[planID]
	if stored == nil {
		for _, output := range outputs {
			if s.occupied[output.ID] != 0 {
				return s.installFailure("InstallPlan", planID, outputs, ErrFileOccupied, "output id is occupied")
			}
		}
		return s.installFailure("InstallPlan", planID, outputs, ErrPlanNotFound, "plan is not active")
	}

	plan := stored.plan
	inputSet := make(map[uint64]bool, len(plan.InputIDs))
	for _, inputID := range plan.InputIDs {
		inputSet[inputID] = true
	}
	for _, output := range outputs {
		if output.Layer != plan.TargetLayer {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "output layer or key range is invalid")
		}
		if existing := s.files[output.ID]; existing != nil && !inputSet[output.ID] {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "output id already exists")
		}
	}

	if plan.Type == PlanMoveDown {
		if len(outputs) != 1 {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "move-down requires exactly one output")
		}
		output := outputs[0]
		intervalMatches := compareKeys(output.MinKey, plan.MinKey) == 0 && compareKeys(output.MaxKey, plan.MaxKey) == 0
		input := s.files[plan.InputIDs[0]]
		if !intervalMatches || (input != nil && output.Bytes != input.Bytes) {
			return s.installFailure("InstallPlan", planID, outputs, ErrInvalidArgument, "move-down output must preserve input interval and size")
		}
	}

	for _, output := range outputs {
		if s.occupied[output.ID] != 0 && !inputSet[output.ID] {
			return s.installFailure("InstallPlan", planID, outputs, ErrFileOccupied, "output id is occupied")
		}
	}

	for _, inputID := range plan.InputIDs {
		if s.files[inputID] == nil {
			return s.installFailure("InstallPlan", planID, outputs, ErrFileNotFound, "plan input is missing")
		}
	}

	target := &s.layers[plan.TargetLayer]
	for _, output := range outputs {
		for _, existing := range target.tree.overlaps(output.MinKey, output.MaxKey) {
			if !inputSet[existing.ID] && intervalsOverlapStrict(output.MinKey, output.MaxKey, existing.MinKey, existing.MaxKey) {
				return s.installFailure("InstallPlan", planID, outputs, ErrLayerInvariant, "output overlaps an unconsumed target file")
			}
		}
	}
	for i := 0; i < len(outputs); i++ {
		for j := i + 1; j < len(outputs); j++ {
			if intervalsOverlapStrict(outputs[i].MinKey, outputs[i].MaxKey, outputs[j].MinKey, outputs[j].MaxKey) {
				return s.installFailure("InstallPlan", planID, outputs, ErrLayerInvariant, "output files overlap each other")
			}
		}
	}

	installed := make([]*File, 0, len(outputs))
	for _, output := range outputs {
		installed = append(installed, cloneFile(output))
	}
	var installedMax []byte
	for _, inputID := range plan.InputIDs {
		file := s.files[inputID]
		if installedMax == nil || compareKeys(file.MaxKey, installedMax) > 0 {
			installedMax = file.MaxKey
		}
		layer := &s.layers[file.Layer]
		delete(s.files, file.ID)
		layer.totalBytes.Sub(layer.totalBytes, uint64Big(file.Bytes))
		if file.Layer == 0 {
			layer.files = removeFileByID(layer.files, file.ID)
		} else {
			layer.files = removeFileByID(layer.files, file.ID)
			layer.tree.remove(file)
		}
	}

	for _, output := range installed {
		s.files[output.ID] = output
		target.files = append(target.files, output)
		target.tree.insert(output)
		target.totalBytes.Add(target.totalBytes, uint64Big(output.Bytes))
	}
	sort.Slice(target.files, func(i, j int) bool { return layerFileOrder(target.files[i], target.files[j]) < 0 })
	s.layers[plan.SourceLayer].lastEnd = append([]byte(nil), installedMax...)

	for _, inputID := range plan.InputIDs {
		delete(s.occupied, inputID)
	}
	delete(s.plans, planID)
	s.logger.Log("InstallPlan", map[string]any{"planID": planID, "outputs": outputs}, map[string]any{"installed": installed, "lastEnd": installedMax}, "atomic install succeeded")
	return nil
}

func (s *Service) CancelPlan(planID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if planID == 0 {
		return s.installFailure("CancelPlan", planID, nil, ErrInvalidArgument, "plan id is required")
	}
	stored := s.plans[planID]
	if stored == nil {
		return s.installFailure("CancelPlan", planID, nil, ErrPlanNotFound, "plan is not active")
	}
	for _, inputID := range stored.plan.InputIDs {
		delete(s.occupied, inputID)
	}
	delete(s.plans, planID)
	s.logger.Log("CancelPlan", planID, nil, "cancelled without changing last-end")
	return nil
}

func (s *Service) installFailure(operation string, planID uint64, outputs []File, err error, reason string) error {
	s.logger.Log(operation, map[string]any{"planID": planID, "outputs": outputs}, errorOutput(err), reason)
	return err
}

func removeFileByID(files []*File, id uint64) []*File {
	for index, file := range files {
		if file.ID == id {
			return append(files[:index], files[index+1:]...)
		}
	}
	return files
}

func uint64Big(value uint64) *big.Int {
	return new(big.Int).SetUint64(value)
}
