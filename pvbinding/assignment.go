package pvbinding

import "sort"

type forcedPair struct {
	claimIndex  int
	volumeIndex int
}

func (c *Controller) assignDelayed(claims []*claimRecord, nodeName string) (map[*claimRecord]*volumeRecord, bool) {
	claimNames := make([]*claimRecord, len(claims))
	copy(claimNames, claims)
	sort.Slice(claimNames, func(i, j int) bool {
		return claimNames[i].claim.Name < claimNames[j].claim.Name
	})

	volumeByKey := make(map[string]int)
	volumeNames := make([]*volumeRecord, 0)
	edges := make(map[[2]int]int64)
	for claimIndex, claim := range claimNames {
		classVolumes := c.byClass[claim.claim.StorageClass]
		for _, volume := range classVolumes {
			if !isCandidate(volume, claim, nodeName) {
				continue
			}
			volumeIndex, ok := volumeByKey[volume.volume.Name]
			if !ok {
				volumeIndex = len(volumeNames)
				volumeByKey[volume.volume.Name] = volumeIndex
				volumeNames = append(volumeNames, volume)
			}
			edges[[2]int{claimIndex, volumeIndex}] = volume.volume.Capacity - claim.claim.RequestedCapacity
		}
	}

	_, minWaste, ok := minCostMatching(edges, len(claimNames), len(volumeNames))
	if !ok {
		return nil, false
	}

	fixedClaims := make(map[int]struct{})
	fixedVolumes := make(map[int]struct{})
	forced := make([]forcedPair, 0)
	for claimIndex := range claimNames {
		allowed := make([]int, 0)
		for edge := range edges {
			if edge[0] != claimIndex {
				continue
			}
			if _, fixed := fixedClaims[edge[0]]; fixed {
				continue
			}
			if _, fixed := fixedVolumes[edge[1]]; fixed {
				continue
			}
			allowed = append(allowed, edge[1])
		}
		sort.Slice(allowed, func(i, j int) bool {
			return volumeNames[allowed[i]].volume.Name < volumeNames[allowed[j]].volume.Name
		})
		for _, volumeIndex := range allowed {
			pairs := append(forced, forcedPair{claimIndex: claimIndex, volumeIndex: volumeIndex})
			if _, _, feasible := minCostMatchingWithForced(edges, len(claimNames), len(volumeNames), pairs, minWaste); feasible {
				forced = pairs
				fixedClaims[claimIndex] = struct{}{}
				fixedVolumes[volumeIndex] = struct{}{}
				break
			}
		}
		if _, ok := fixedClaims[claimIndex]; !ok {
			return nil, false
		}
	}

	result := make(map[*claimRecord]*volumeRecord, len(forced))
	for _, pair := range forced {
		result[claimNames[pair.claimIndex]] = volumeNames[pair.volumeIndex]
	}
	return result, true
}

func minCostMatching(edges map[[2]int]int64, claimCount, volumeCount int) (map[int]int, int64, bool) {
	return minCostMatchingWithForced(edges, claimCount, volumeCount, nil, -1)
}

func minCostMatchingWithForced(edges map[[2]int]int64, claimCount, volumeCount int, forced []forcedPair, targetWaste int64) (map[int]int, int64, bool) {
	network := newFlowNetwork(claimCount, volumeCount)
	fixedClaims := make(map[int]struct{})
	fixedVolumes := make(map[int]struct{})
	for _, pair := range forced {
		edge := [2]int{pair.claimIndex, pair.volumeIndex}
		if _, ok := edges[edge]; !ok {
			return nil, 0, false
		}
		fixedClaims[pair.claimIndex] = struct{}{}
		fixedVolumes[pair.volumeIndex] = struct{}{}
	}
	for edge, cost := range edges {
		if _, fixed := fixedClaims[edge[0]]; fixed {
			if forcedVolume := forcedVolumeForClaim(forced, edge[0]); forcedVolume != edge[1] {
				continue
			}
		}
		if _, fixed := fixedVolumes[edge[1]]; fixed {
			usedByFixed := false
			for _, pair := range forced {
				if pair.volumeIndex == edge[1] && pair.claimIndex == edge[0] {
					usedByFixed = true
				}
			}
			if !usedByFixed {
				continue
			}
		}
		network.addEdge(network.claimBase+edge[0], network.volumeBase+edge[1], 1, cost)
	}
	flow, cost := network.minCostMaxFlow(network.source, network.sink, int64(claimCount))
	if flow != int64(claimCount) {
		return nil, 0, false
	}
	if targetWaste >= 0 && cost != targetWaste {
		return nil, cost, false
	}
	return network.matching(), cost, true
}

func forcedVolumeForClaim(forced []forcedPair, claimIndex int) int {
	for _, pair := range forced {
		if pair.claimIndex == claimIndex {
			return pair.volumeIndex
		}
	}
	return -1
}
