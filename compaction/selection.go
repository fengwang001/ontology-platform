package compaction

import (
	"errors"
	"math/big"
	"sort"
)

func validateConfig(config Config) error {
	if config.Layers < 2 {
		return errors.New("layers must be at least two")
	}
	if config.ZeroTrigger == 0 || config.FirstNonZeroTarget == 0 || config.TargetMultiplier == 0 {
		return errors.New("threshold, target, and multiplier must be positive")
	}
	return nil
}

func scoreLayer(layer int, fileCount int, totalBytes *big.Int, config Config) *big.Rat {
	if layer == 0 {
		return new(big.Rat).SetFrac(big.NewInt(int64(fileCount)), new(big.Int).SetUint64(config.ZeroTrigger))
	}
	target := new(big.Int).SetUint64(config.FirstNonZeroTarget)
	multiplier := new(big.Int).SetUint64(config.TargetMultiplier)
	target.Mul(target, new(big.Int).Exp(multiplier, big.NewInt(int64(layer-1)), nil))
	return new(big.Rat).SetFrac(new(big.Int).Set(totalBytes), target)
}

func rankScores(scores []LayerScore) []LayerScore {
	ranked := append([]LayerScore(nil), scores...)
	sort.SliceStable(ranked, func(i, j int) bool {
		comparison := ranked[i].Score.Cmp(ranked[j].Score)
		if comparison != 0 {
			return comparison > 0
		}
		return ranked[i].Layer < ranked[j].Layer
	})
	return ranked
}
