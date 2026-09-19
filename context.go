package coercion

type context struct {
	mode         Mode
	target       Target
	sliceNil     bool
	errors       Errors
	degradations []Degradation
}

func (c *context) report(err *Error) {
	if c.mode == Lenient {
		if err.skipped {
			c.errors = append(c.errors, err)
			return
		}
		c.degradations = append(c.degradations, Degradation{
			Category:  err.Category,
			Index:     err.Index,
			hasIndex:  err.hasIndex,
			Original:  err.Original,
			Converted: err.Converted,
			Detail:    err.Detail,
			Skipped:   err.skipped,
		})
		return
	}
	c.errors = append(c.errors, err)
}

func (c *context) result(state State, value any) Result {
	degradations := make([]Degradation, len(c.degradations))
	copy(degradations, c.degradations)
	return Result{
		State:        state,
		Value:        value,
		Degradations: degradations,
	}
}

func (c *context) err() error {
	if len(c.errors) == 0 {
		return nil
	}
	return c.errors
}
