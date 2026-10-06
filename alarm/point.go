package alarm

import "sort"

const ChatterReason = "震荡"

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type point struct {
	config               PointConfig
	state                State
	lastActivation       int64
	suppressedUntil      int64
	suppressionVersion   int64
	suppressionReason    string
	conditionInhibited   bool
	disabled             bool
	unacknowledgedOnShow bool
	chatter              chatterWindow
}

func (p *point) suppressed(at int64) bool {
	return p.suppressedUntil > at
}

func (p *point) hidden(at int64) bool {
	return p.suppressed(at) || p.conditionInhibited || p.disabled
}

func (p *point) activeEntry() ActiveAlarm {
	state := p.state
	if p.unacknowledgedOnShow && (state == ActiveAcknowledged || state == ReturnedUnacknowledged) {
		state = ActiveUnacknowledged
	}
	return ActiveAlarm{
		PointID:        p.config.ID,
		Priority:       p.config.Priority,
		State:          state,
		LastActivation: p.lastActivation,
	}
}
