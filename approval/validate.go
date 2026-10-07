package approval

func validateLicenseType(spec LicenseType) error {
	if spec.ID == "" || len(spec.Stages) == 0 {
		return ErrInvalidArgument
	}
	defined := make(map[string]struct{}, len(spec.Stages))
	for _, stage := range spec.Stages {
		if stage.ID == "" || stage.Department == "" || stage.TimeLimit <= 0 {
			return ErrInvalidArgument
		}
		if _, exists := defined[stage.ID]; exists {
			return ErrInvalidArgument
		}
		if stage.CorrectionLimit < 0 || stage.CorrectionDays < 0 {
			return ErrInvalidArgument
		}
		if stage.CorrectionLimit > 0 && stage.CorrectionDays <= 0 {
			return ErrInvalidArgument
		}
		defined[stage.ID] = struct{}{}
	}

	visitState := make(map[string]uint8, len(spec.Stages))
	var visit func(string) bool
	visit = func(id string) bool {
		switch visitState[id] {
		case 1:
			return false
		case 2:
			return true
		}
		visitState[id] = 1
		for _, stage := range spec.Stages {
			if stage.ID != id {
				continue
			}
			seen := make(map[string]struct{}, len(stage.Prerequisites))
			for _, prerequisite := range stage.Prerequisites {
				if _, ok := defined[prerequisite]; !ok {
					return false
				}
				if _, duplicate := seen[prerequisite]; duplicate {
					return false
				}
				seen[prerequisite] = struct{}{}
				if prerequisite == id || !visit(prerequisite) {
					return false
				}
			}
		}
		visitState[id] = 2
		return true
	}

	for _, stage := range spec.Stages {
		if !visit(stage.ID) {
			return ErrInvalidArgument
		}
	}
	return nil
}
