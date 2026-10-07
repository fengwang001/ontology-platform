package tolling

import "time"

type capResult struct {
	charge        Money
	refund        Money
	uncollected   Money
	blockedRefund Money
	collected     Money
}

type monthLedger struct {
	collected      Money
	cappedCharges  Money
	blockedRefunds Money
}

func monthKey(at time.Time, loc *time.Location) string {
	return at.In(loc).Format("2006-01")
}

func (m *monthLedger) apply(rawTarget, alreadyCollected, priorUncollected Money, cap Money) capResult {
	result := capResult{collected: m.collected, uncollected: priorUncollected}
	if rawTarget > alreadyCollected {
		delta := rawTarget - alreadyCollected
		available := cap - m.collected
		if available < 0 {
			available = 0
		}
		result.charge = minMoney(delta, available)
		result.uncollected = priorUncollected + delta - result.charge
		m.collected += result.charge
		m.cappedCharges += delta - result.charge
		return result
	}
	if rawTarget < alreadyCollected {
		decrease := alreadyCollected - rawTarget
		released := minMoney(decrease, priorUncollected)
		requestedRefund := decrease - released
		result.refund = minMoney(requestedRefund, m.collected)
		result.blockedRefund = requestedRefund - result.refund
		released += result.blockedRefund
		result.uncollected = priorUncollected - released
		m.collected -= result.refund
		m.cappedCharges -= released
		m.blockedRefunds += result.blockedRefund
		return result
	}
	return result
}

func minMoney(a, b Money) Money {
	if a < b {
		return a
	}
	return b
}
