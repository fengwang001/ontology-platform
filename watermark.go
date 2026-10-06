package thinpool

import "math/bits"

type WaterLevel string

const (
	LevelNormal   WaterLevel = "normal"
	LevelWarning  WaterLevel = "warning"
	LevelCritical WaterLevel = "critical"
)

type Event struct {
	Sequence  uint64
	OldLevel  WaterLevel
	NewLevel  WaterLevel
	Allocated uint64
}

func waterLevel(allocated, physicalBlocks, warningPercent, criticalPercent uint64) WaterLevel {
	if compareProducts(allocated, 100, physicalBlocks, criticalPercent) >= 0 {
		return LevelCritical
	}
	if compareProducts(allocated, 100, physicalBlocks, warningPercent) >= 0 {
		return LevelWarning
	}
	return LevelNormal
}

func compareProducts(a, b, c, d uint64) int {
	aHi, aLo := bits.Mul64(a, b)
	cHi, cLo := bits.Mul64(c, d)
	if aHi != cHi {
		if aHi < cHi {
			return -1
		}
		return 1
	}
	if aLo != cLo {
		if aLo < cLo {
			return -1
		}
		return 1
	}
	return 0
}
