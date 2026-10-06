package qc

import "math"

type levelRuleState struct {
	previousSide int
	overTwoRun   int
	overOneRun   int
	sameSideRun  int
}

func newLevelRuleState() *levelRuleState {
	return &levelRuleState{}
}

func (s *levelRuleState) reset() {
	*s = levelRuleState{}
}

func (s *levelRuleState) evaluate(spec LevelSpec, value int64) (int64, map[int]bool) {
	deviation := value - spec.Target
	side := 0
	if deviation > 0 {
		side = 1
	} else if deviation < 0 {
		side = -1
	}

	overThree := magnitudeExceeds(value, spec.Target, spec.StandardDeviation, 3)
	overTwo := magnitudeExceeds(value, spec.Target, spec.StandardDeviation, 2)
	overOne := magnitudeExceeds(value, spec.Target, spec.StandardDeviation, 1)

	nextOverTwo := 0
	nextOverOne := 0
	nextSameSide := 0
	if overTwo {
		if side == s.previousSide {
			nextOverTwo = s.overTwoRun + 1
		} else {
			nextOverTwo = 1
		}
	}
	if overOne {
		if side == s.previousSide {
			nextOverOne = s.overOneRun + 1
		} else {
			nextOverOne = 1
		}
	}
	if side != 0 {
		if side == s.previousSide {
			nextSameSide = s.sameSideRun + 1
		} else {
			nextSameSide = 1
		}
	}

	triggered := make(map[int]bool, 4)
	if overThree {
		triggered[1] = true
	}
	if nextOverTwo >= 2 {
		triggered[2] = true
	}
	if nextOverOne >= 4 {
		triggered[4] = true
	}
	if nextSameSide >= 10 {
		triggered[5] = true
	}

	s.previousSide = side
	s.overTwoRun = nextOverTwo
	s.overOneRun = nextOverOne
	s.sameSideRun = nextSameSide

	return deviation, triggered
}

func magnitudeExceeds(value, target, standardDeviation, multiplier int64) bool {
	if standardDeviation > math.MaxInt64/multiplier {
		return false
	}
	deviation := value - target
	limit := standardDeviation * multiplier
	return deviation > limit || deviation < -limit
}
