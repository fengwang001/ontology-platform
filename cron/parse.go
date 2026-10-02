package cron

// Spec is a parsed five-field cron expression.
//
// The five sets hold the allowed values of minute, hour, day-of-month,
// month and day-of-week (0 = Sunday ... 6 = Saturday). dayWild and
// weekWild record whether the corresponding field text starts with '*'.
type Spec struct {
	minutes  [60]bool
	hours    [24]bool
	days     [31]bool
	months   [12]bool
	weekdays [7]bool

	dayWild  bool
	weekWild bool

	// daySteps is the number of whole days examined by the most recent
	// NextFire call. It exists to prove the algorithm advances day by
	// day instead of scanning individual minutes.
	daySteps int
}

// MatchesMinute reports whether minute mi is in the minute set.
func (s *Spec) MatchesMinute(mi int) bool {
	return s.minutes[mi]
}

// MatchesHour reports whether hour h is in the hour set.
func (s *Spec) MatchesHour(h int) bool {
	return s.hours[h]
}

// DaySteps returns the number of whole days examined by the most recent
// successful or unsuccessful NextFire call.
func (s *Spec) DaySteps() int {
	return s.daySteps
}

// fieldDef describes one cron field: its zero-based minimum value,
// its (inclusive) maximum value and the count of distinct values.
type fieldDef struct {
	min, max int
}

var fieldDefs = [5]fieldDef{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week (7 is explicitly illegal)
}

// Parse parses a five-field cron expression separated by single spaces.
func Parse(spec string) (*Spec, error) {
	fields := splitFields(spec)
	if len(fields) != 5 {
		return nil, ErrSyntax
	}
	s := &Spec{}
	sets := [5][]bool{
		make([]bool, 60),
		make([]bool, 24),
		make([]bool, 31),
		make([]bool, 12),
		make([]bool, 7),
	}
	wild := [5]bool{}
	for fi := 0; fi < 5; fi++ {
		text := fields[fi]
		if len(text) > 0 && text[0] == '*' {
			wild[fi] = true
		}
		items := splitItems(text)
		for _, item := range items {
			if err := parseItem(item, fieldDefs[fi], sets[fi]); err != nil {
				return nil, err
			}
		}
	}
	copy(s.minutes[:], sets[0])
	copy(s.hours[:], sets[1])
	copy(s.days[:], sets[2])
	copy(s.months[:], sets[3])
	copy(s.weekdays[:], sets[4])
	s.dayWild = wild[2]
	s.weekWild = wild[4]
	return s, nil
}

// splitFields splits on a single space without collapsing runs:
// consecutive spaces produce empty fields, which later fail syntax.
func splitFields(spec string) []string {
	fields := []string{}
	start := 0
	for i := 0; i < len(spec); i++ {
		if spec[i] == ' ' {
			fields = append(fields, spec[start:i])
			start = i + 1
		}
	}
	fields = append(fields, spec[start:])
	return fields
}

// splitItems splits one field on commas; an empty field yields one
// empty item which is reported as a syntax error.
func splitItems(text string) []string {
	items := []string{}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == ',' {
			items = append(items, text[start:i])
			start = i + 1
		}
	}
	items = append(items, text[start:])
	return items
}

// parseItem parses one comma-separated item into the value set.
//
// Accepted forms: *, a, a-b, */s, a/s, a-b/s.
// For each item the checks are, in order: syntax (empty or illegal
// characters), value out of range, reversed range, illegal step.
func parseItem(item string, def fieldDef, set []bool) error {
	if item == "" {
		return ErrSyntax
	}
	count := def.max - def.min + 1
	// A field text may carry the wildcard flag, but an individual item
	// containing '*' must be exactly '*' or '*/s': "*,1", "1,*" or
	// "a-*" forms are not legal list items.
	for i := 0; i < len(item); i++ {
		if item[i] == '*' && item != "*" && !(len(item) >= 2 && item[0] == '*' && item[1] == '/') {
			return ErrSyntax
		}
	}

	// Split off an optional step.
	var rangeText, stepText string
	slash := -1
	for i := 0; i < len(item); i++ {
		if item[i] == '/' {
			if slash >= 0 {
				return ErrSyntax
			}
			slash = i
		}
	}
	if slash >= 0 {
		rangeText = item[:slash]
		stepText = item[slash+1:]
	} else {
		rangeText = item
	}

	lo, hi := def.min, def.max
	if rangeText == "*" {
		// whole range
	} else {
		dash := -1
		for i := 0; i < len(rangeText); i++ {
			if rangeText[i] == '-' {
				if dash >= 0 {
					return ErrSyntax
				}
				dash = i
			}
		}
		if dash >= 0 {
			loStr, hiStr := rangeText[:dash], rangeText[dash+1:]
			a, ok1 := parseNonNegInt(loStr)
			b, ok2 := parseNonNegInt(hiStr)
			if !ok1 || !ok2 {
				return ErrSyntax
			}
			if a < def.min || a > def.max || b < def.min || b > def.max {
				return ErrValueOutOfRange
			}
			if a > b {
				return ErrReversedRange
			}
			lo, hi = a, b
		} else {
			a, ok := parseNonNegInt(rangeText)
			if !ok {
				return ErrSyntax
			}
			if a < def.min || a > def.max {
				return ErrValueOutOfRange
			}
			lo = a
			if slash >= 0 {
				// a/s means a .. field maximum with step s.
				hi = def.max
			} else {
				hi = a
			}
		}
	}

	step := 1
	if slash >= 0 {
		s, ok := parseNonNegInt(stepText)
		if !ok {
			return ErrSyntax
		}
		if s < 1 || s > count {
			return ErrInvalidStep
		}
		step = s
	}

	for v := lo; v <= hi; v += step {
		set[v-def.min] = true
	}
	return nil
}

// parseNonNegInt parses a strict non-negative integer: every byte must
// be an ASCII digit and the string must be non-empty.
func parseNonNegInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	return v, true
}
