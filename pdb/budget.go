package pdb

// validateValue checks a parsed minAvailable/maxUnavailable value.
func validateValue(v *Value) error {
	if v == nil {
		return errf(ReasonInvalidArgument, PodRef{}, -1, "missing budget value")
	}
	if v.Amount < 0 {
		return errf(ReasonInvalidArgument, PodRef{}, -1, "budget value must be non-negative")
	}
	if v.IsPercent && v.Amount > 100 {
		return errf(ReasonInvalidArgument, PodRef{}, -1, "percentage must be in [0,100]")
	}
	return nil
}

// validateSpec checks structural validity of a budget definition.
func validateSpec(b BudgetSpec) error {
	if b.Name == "" || b.Namespace == "" {
		return errf(ReasonInvalidArgument, PodRef{}, -1, "budget name and namespace are required")
	}
	minSet, maxSet := b.MinAvailable != nil, b.MaxUnavailable != nil
	if minSet == maxSet {
		return errf(ReasonInvalidArgument, PodRef{Namespace: b.Namespace, UID: b.Name}, -1,
			"exactly one of minAvailable/maxUnavailable must be set")
	}
	if minSet {
		if err := validateValue(b.MinAvailable); err != nil {
			return err
		}
	} else {
		if err := validateValue(b.MaxUnavailable); err != nil {
			return err
		}
	}
	return nil
}

// resolveValue parses a value against the expected total.
//   - absolute: value as-is
//   - minAvailable percentage: ceil(expected * p / 100)
//   - maxUnavailable percentage: floor(expected * p / 100)
func resolveValue(v *Value, expected int, roundUp bool) int {
	if !v.IsPercent {
		return v.Amount
	}
	num := expected * v.Amount
	if roundUp {
		// ceil(num/100), valid for non-negative inputs
		return (num + 99) / 100
	}
	return num / 100
}

// requiredReady returns the number of ready pods that must be maintained.
// The result is only clamped at zero; an absolute minAvailable larger than the
// expected total is reported as-is (the allowance then simply becomes zero).
func requiredReady(b *budgetEntry) int {
	expected := b.expected
	req := 0
	if b.spec.MinAvailable != nil {
		req = resolveValue(b.spec.MinAvailable, expected, true)
	} else {
		req = expected - resolveValue(b.spec.MaxUnavailable, expected, false)
	}
	if req < 0 {
		req = 0
	}
	return req
}

// disruptionAllowed is current ready minus required, clamped at zero.
func disruptionAllowed(b *budgetEntry) int {
	allow := b.ready - requiredReady(b)
	if allow < 0 {
		return 0
	}
	return allow
}

// selectorEmpty reports whether the selector matches no pod.
func selectorEmpty(sel Selector) bool { return len(sel) == 0 }
