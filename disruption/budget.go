package disruption

// validateIntOrPct validates the minAvailable/maxUnavailable payload.
func validateIntOrPct(v *IntOrPct, field string) error {
	if v == nil {
		return errf(KindInvalidArgument, field+" is required")
	}
	if v.Percent {
		if v.Value < 0 || v.Value > 100 {
			return errf(KindInvalidArgument, field+" percent must be in [0,100]")
		}
	} else if v.Value < 0 {
		return errf(KindInvalidArgument, field+" must be non-negative")
	}
	return nil
}

// validateBudget checks structural validity: exactly one of minAvailable and
// maxUnavailable must be set; ids must be non-empty.
func validateBudget(b PodDisruptionBudget) error {
	if b.ID.Namespace == "" || b.ID.Name == "" {
		return errf(KindInvalidArgument, "budget namespace and name are required")
	}
	if (b.MinAvailable == nil) == (b.MaxUnavail == nil) {
		return errf(KindInvalidArgument, "exactly one of minAvailable or maxUnavailable must be set")
	}
	if b.MinAvailable != nil {
		if err := validateIntOrPct(b.MinAvailable, "minAvailable"); err != nil {
			return err
		}
	} else {
		if err := validateIntOrPct(b.MaxUnavail, "maxUnavailable"); err != nil {
			return err
		}
	}
	return nil
}

// selectorMatches reports whether the pod labels satisfy every equality
// requirement. An empty/nil selector matches nothing.
func selectorMatches(sel Selector, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// resolveScaled converts a percentage of expected:
//   - minAvailable rounds UP (ceil);
//   - maxUnavailable rounds DOWN (floor).
//
// expected is non-negative and pct in [0,100].
func resolvePct(pct, expected int, ceil bool) int {
	// ceil(pct*expected/100) = (pct*expected + 99) / 100 for non-negatives.
	num := pct * expected
	if ceil {
		return (num + 99) / 100
	}
	return num / 100
}

// requiredReady computes the number of matched pods that must stay ready:
//   - minAvailable: the parsed absolute value, or ceil(percent * expected);
//   - maxUnavailable: expected - parsedValue (floor for percentages),
//     clamped at zero.
func requiredReady(b PodDisruptionBudget, expected int) int {
	if b.MinAvailable != nil {
		v := b.MinAvailable
		if !v.Percent {
			return v.Value
		}
		return resolvePct(v.Value, expected, true)
	}
	v := b.MaxUnavail
	var allowedUnavailable int
	if v.Percent {
		allowedUnavailable = resolvePct(v.Value, expected, false)
	} else {
		allowedUnavailable = v.Value
	}
	r := expected - allowedUnavailable
	if r < 0 {
		return 0
	}
	return r
}

// allowance is the disruption allowance: currentReady - required, >= 0.
func allowance(currentReady, required int) int {
	d := currentReady - required
	if d < 0 {
		return 0
	}
	return d
}
