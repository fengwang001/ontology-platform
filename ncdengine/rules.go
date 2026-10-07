package ncdengine

import "sort"

func nextLevel(initialLevel int, claims []Claim, protected bool, config Config) int {
	atFault := make([]Claim, 0, len(claims))
	for _, claim := range claims {
		if claim.Liability >= config.AtFaultThreshold {
			atFault = append(atFault, claim)
		}
	}
	sort.SliceStable(atFault, func(i, j int) bool {
		if atFault[i].AccidentDay == atFault[j].AccidentDay {
			return atFault[i].ID < atFault[j].ID
		}
		return atFault[i].AccidentDay < atFault[j].AccidentDay
	})

	if len(atFault) >= 3 {
		return 0
	}

	chargeable := len(atFault)
	if protected && chargeable > 0 {
		chargeable--
	}
	if chargeable == 0 {
		if initialLevel >= config.MaxLevel {
			return config.MaxLevel
		}
		return initialLevel + 1
	}

	level := initialLevel - chargeable*2
	if level < 0 {
		return 0
	}
	return level
}

func withinRenewalWindow(endDay int, day int, graceDays int) bool {
	return day >= endDay-30 && day <= endDay+graceDays
}

func validConfig(config Config) bool {
	if config.RenewalGraceDays <= 0 || config.MaxLevel <= 0 {
		return false
	}
	if config.AtFaultThreshold < 0 || config.AtFaultThreshold > 100 {
		return false
	}
	if config.ProtectionStart < 0 || config.ProtectionStart > config.MaxLevel {
		return false
	}
	if len(config.Premiums) != config.MaxLevel+1 {
		return false
	}
	for _, premium := range config.Premiums {
		if premium < 0 {
			return false
		}
	}
	return true
}
