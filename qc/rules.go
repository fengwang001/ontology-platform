package qc

type Rule string

const (
	Rule1 Rule = "rule_1"
	Rule2 Rule = "rule_2"
	Rule3 Rule = "rule_3"
	Rule4 Rule = "rule_4"
	Rule5 Rule = "rule_5"
)

type RunOutcome string

const (
	OutcomeNormal  RunOutcome = "normal"
	OutcomeWarning RunOutcome = "warning"
	OutcomeReject  RunOutcome = "out_of_control"
)

type LevelResult struct {
	Value     int64
	Target    int64
	SD        int64
	Deviation int64
}

type QCRecord struct {
	Time       int64
	Instrument string
	Assay      string
	Low        LevelResult
	High       LevelResult
	Triggered  []Rule
	Outcome    RunOutcome
}

type levelConfig struct {
	target int64
	sd     int64
}

type levelState struct {
	lastSign      int
	sideStreak    int
	overOneStreak int
	overTwoStreak int
}

type levelEvaluation struct {
	result LevelResult
	sign   int
	next   levelState
}

func evaluateRun(previous *assayState, lowValue, highValue int64) ([]Rule, RunOutcome) {
	low := evaluateLevel(previous.low, previous.lowCfg, lowValue)
	high := evaluateLevel(previous.high, previous.highCfg, highValue)

	triggered := make([]Rule, 0, 5)
	if exceeds(low.result.Deviation, 3, low.result.SD) || exceeds(high.result.Deviation, 3, high.result.SD) {
		triggered = append(triggered, Rule1)
	}
	if previous.low.overTwoStreak >= 1 && low.sign == previous.low.lastSign && exceeds(low.result.Deviation, 2, low.result.SD) {
		triggered = append(triggered, Rule2)
	}
	if previous.high.overTwoStreak >= 1 && high.sign == previous.high.lastSign && exceeds(high.result.Deviation, 2, high.result.SD) {
		if len(triggered) == 0 || triggered[len(triggered)-1] != Rule2 {
			triggered = append(triggered, Rule2)
		}
	}
	if (low.sign > 0 && high.sign < 0 && exceeds(low.result.Deviation, 2, low.result.SD) && exceeds(high.result.Deviation, 2, high.result.SD)) ||
		(low.sign < 0 && high.sign > 0 && exceeds(low.result.Deviation, 2, low.result.SD) && exceeds(high.result.Deviation, 2, high.result.SD)) {
		triggered = append(triggered, Rule3)
	}
	if low.result.Deviation != 0 && previous.low.overOneStreak >= 3 && low.sign == previous.low.lastSign && exceeds(low.result.Deviation, 1, low.result.SD) {
		triggered = append(triggered, Rule4)
	}
	if high.result.Deviation != 0 && previous.high.overOneStreak >= 3 && high.sign == previous.high.lastSign && exceeds(high.result.Deviation, 1, high.result.SD) {
		if len(triggered) == 0 || triggered[len(triggered)-1] != Rule4 {
			triggered = append(triggered, Rule4)
		}
	}
	if low.result.Deviation != 0 && previous.low.sideStreak >= 9 && low.sign == previous.low.lastSign {
		triggered = append(triggered, Rule5)
	}
	if high.result.Deviation != 0 && previous.high.sideStreak >= 9 && high.sign == previous.high.lastSign {
		if len(triggered) == 0 || triggered[len(triggered)-1] != Rule5 {
			triggered = append(triggered, Rule5)
		}
	}

	outcome := OutcomeNormal
	if len(triggered) > 0 {
		outcome = OutcomeReject
	} else if exceeds(low.result.Deviation, 2, low.result.SD) || exceeds(high.result.Deviation, 2, high.result.SD) {
		outcome = OutcomeWarning
	}
	previous.low = low.next
	previous.high = high.next
	return triggered, outcome
}

func evaluateLevel(state levelState, config levelConfig, value int64) levelEvaluation {
	deviation := value - config.target
	sign := deviationSign(deviation)
	evaluation := levelEvaluation{
		result: LevelResult{
			Value:     value,
			Target:    config.target,
			SD:        config.sd,
			Deviation: deviation,
		},
		sign: sign,
	}
	if sign == 0 {
		state.lastSign = 0
		state.sideStreak = 0
		state.overOneStreak = 0
		state.overTwoStreak = 0
		evaluation.next = state
		return evaluation
	}
	if sign == state.lastSign {
		state.sideStreak++
		state.overOneStreak = incrementConditionalStreak(state.overOneStreak, exceeds(deviation, 1, config.sd))
		state.overTwoStreak = incrementConditionalStreak(state.overTwoStreak, exceeds(deviation, 2, config.sd))
	} else {
		state.lastSign = sign
		state.sideStreak = 1
		state.overOneStreak = incrementConditionalStreak(0, exceeds(deviation, 1, config.sd))
		state.overTwoStreak = incrementConditionalStreak(0, exceeds(deviation, 2, config.sd))
	}
	evaluation.next = state
	return evaluation
}

func incrementConditionalStreak(streak int, condition bool) int {
	if !condition {
		return 0
	}
	return streak + 1
}

func deviationSign(deviation int64) int {
	if deviation > 0 {
		return 1
	}
	if deviation < 0 {
		return -1
	}
	return 0
}

func exceeds(deviation int64, multiplier int64, sd int64) bool {
	var magnitude uint64
	if deviation < 0 {
		magnitude = uint64(-(deviation + 1)) + 1
	} else {
		magnitude = uint64(deviation)
	}
	return magnitude > uint64(multiplier)*uint64(sd)
}
