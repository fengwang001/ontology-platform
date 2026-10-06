package pvbinding

import "sort"

func (o *oracle) bindDelayed(node string, names []string) ErrorCode {
	claims := make([]*oracleClaim, 0, len(names))
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			return ErrCodeInvalidArgument
		}
		seen[name] = true
		claim, ok := o.claims[name]
		if !ok {
			return ErrCodeNotFound
		}
		if claim.spec.BindingMode != BindingDelayed || claim.boundVolume != "" {
			return ErrCodeInvalidArgument
		}
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool {
		return claims[i].spec.Name < claims[j].spec.Name
	})

	candidates := make([][]string, len(claims))
	for i, claim := range claims {
		volumes := make([]string, 0)
		for _, volume := range o.volumes {
			if oracleCandidate(volume, claim, node) {
				volumes = append(volumes, volume.spec.Name)
			}
		}
		sort.Strings(volumes)
		candidates[i] = volumes
	}

	best := enumerateBestJoint(o, claims, candidates)
	if best == nil {
		return ErrCodeNoMatchingVolume
	}
	for i, volumeName := range best {
		claims[i].boundVolume = volumeName
		o.volumes[volumeName].state = VolumeBound
	}
	return 0
}

func enumerateBestJoint(o *oracle, claims []*oracleClaim, candidates [][]string) []string {
	used := make(map[string]bool)
	current := make([]string, len(claims))
	var best []string
	var bestWaste int64
	var hasBest bool

	var visit func(int)
	visit = func(index int) {
		if index == len(claims) {
			waste := int64(0)
			for i, volumeName := range current {
				waste += o.volumes[volumeName].spec.Capacity - claims[i].spec.RequestedCapacity
			}
			if !hasBest || jointIsBetter(current, waste, best, bestWaste) {
				best = append([]string(nil), current...)
				bestWaste = waste
				hasBest = true
			}
			return
		}
		for _, volumeName := range candidates[index] {
			if used[volumeName] {
				continue
			}
			used[volumeName] = true
			current[index] = volumeName
			visit(index + 1)
			used[volumeName] = false
		}
	}
	visit(0)
	return best
}

func jointIsBetter(left []string, leftWaste int64, right []string, rightWaste int64) bool {
	if leftWaste != rightWaste {
		return leftWaste < rightWaste
	}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	return false
}
