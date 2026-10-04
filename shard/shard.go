package shard

import "sort"

type Case struct {
	Name        string
	Samples     []int
	Quarantined bool
}

type Plan struct {
	Regular    [][]string
	Quarantine []string
}

func Estimate(samples []int) int {
	if len(samples) == 0 {
		return 0
	}
	total := 0
	for _, duration := range samples {
		total += duration
	}
	return (total + len(samples) - 1) / len(samples)
}

func Build(cases []Case, shardCount int) Plan {
	regular := make([][]string, shardCount)
	sums := make([]int, shardCount)

	known := make([]int, 0, len(cases))
	for _, testCase := range cases {
		if !testCase.Quarantined && len(testCase.Samples) > 0 {
			known = append(known, Estimate(testCase.Samples))
		}
	}
	sort.Ints(known)
	unknownEstimate := 1
	if len(known) > 0 {
		unknownEstimate = known[(len(known)-1)/2]
	}

	type candidate struct {
		name     string
		estimate int
	}
	candidates := make([]candidate, 0, len(cases))
	quarantined := make([]string, 0)
	for _, testCase := range cases {
		if testCase.Quarantined {
			quarantined = append(quarantined, testCase.Name)
			continue
		}
		estimate := unknownEstimate
		if len(testCase.Samples) > 0 {
			estimate = Estimate(testCase.Samples)
		}
		candidates = append(candidates, candidate{name: testCase.Name, estimate: estimate})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].estimate != candidates[j].estimate {
			return candidates[i].estimate > candidates[j].estimate
		}
		return candidates[i].name < candidates[j].name
	})
	for _, item := range candidates {
		target := 0
		for index := 1; index < shardCount; index++ {
			if sums[index] < sums[target] {
				target = index
			}
		}
		regular[target] = append(regular[target], item.name)
		sums[target] += item.estimate
	}

	sort.Strings(quarantined)
	return Plan{Regular: regular, Quarantine: quarantined}
}
