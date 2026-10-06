package pvbinding

import (
	"sort"
	"strings"
)

var candidateCheckCount int

func resetCandidateCheckCount() {
	candidateCheckCount = 0
}

func isCandidate(volume *volumeRecord, claim *claimRecord, nodeName string) bool {
	candidateCheckCount++
	v := &volume.volume
	c := &claim.claim
	if v.State != VolumeAvailable || v.StorageClass != c.StorageClass {
		return false
	}
	if c.HasVolumeName && v.Name != c.VolumeName {
		return false
	}
	if v.Capacity < c.RequestedCapacity {
		return false
	}
	if !containsAll(v.AccessModes, c.AccessModes) {
		return false
	}
	if !matchesLabels(v.Labels, c.Selector) {
		return false
	}
	if v.HasReservation && v.ReservationName != c.Name {
		return false
	}
	if nodeName != "" && v.HasNodeConstraint {
		if _, ok := v.NodeNames[nodeName]; !ok {
			return false
		}
	}
	return true
}

func containsAll(actual map[string]struct{}, required map[string]struct{}) bool {
	for mode := range required {
		if _, ok := actual[mode]; !ok {
			return false
		}
	}
	return true
}

func matchesLabels(labels map[string]string, selector map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func (c *Controller) chooseImmediate(claim *claimRecord) *volumeRecord {
	classVolumes := c.byClass[claim.claim.StorageClass]
	candidates := make([]*volumeRecord, 0)
	for _, volume := range classVolumes {
		if isCandidate(volume, claim, "") {
			candidates = append(candidates, volume)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := &candidates[i].volume
		right := &candidates[j].volume
		if left.Capacity != right.Capacity {
			return left.Capacity < right.Capacity
		}
		return left.Name < right.Name
	})
	return candidates[0]
}

func validateName(name string) bool {
	return strings.TrimSpace(name) != ""
}
