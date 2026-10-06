package smartlocker

func feeAt(policy FeePolicy, storedAt, at int64) int64 {
	if at-storedAt <= policy.FreeDuration {
		return 0
	}
	over := at - storedAt - policy.FreeDuration
	periods := over / policy.Period
	if policy.PeriodFee > 0 && periods > policy.MaximumFee/policy.PeriodFee {
		return policy.MaximumFee
	}
	return periods * policy.PeriodFee
}

func feeDue(policy FeePolicy, storedAt, at, paid int64) int64 {
	due := feeAt(policy, storedAt, at) - paid
	if due < 0 {
		return 0
	}
	return due
}
