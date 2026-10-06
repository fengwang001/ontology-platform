package lease

func validateConfig(config Config) error {
	if config.MinOfferDaysBeforeEnd < 0 ||
		config.MaxOfferDaysBeforeEnd < config.MinOfferDaysBeforeEnd ||
		config.ResponseDays < 0 ||
		config.TerminationNoticeDays < 0 ||
		config.AnnualStepBasisPoints < 0 ||
		config.CapBasisPoints < 0 {
		return invalidArgument("invalid renewal window, response, notice, step, or cap parameter")
	}
	return nil
}

func validateCreateLeaseInput(input CreateLeaseInput) error {
	if input.ID == "" ||
		input.StartDay < 0 ||
		input.EndDay <= input.StartDay ||
		input.MonthlyRentCents <= 0 ||
		input.LastAdjustmentAt > input.StartDay {
		return invalidArgument("invalid lease id, interval, rent, or adjustment date")
	}
	return nil
}

func fullYearsSince(fromDay, nowDay int) int {
	if nowDay < fromDay {
		return 0
	}
	return (nowDay - fromDay) / 365
}

func maximumRentCents(currentRentCents, fullYears, annualStepBasisPoints, capBasisPoints int) int {
	if fullYears <= 0 || annualStepBasisPoints <= 0 {
		return currentRentCents
	}
	rateBasisPoints := fullYears * annualStepBasisPoints
	if capBasisPoints > 0 && rateBasisPoints > capBasisPoints {
		rateBasisPoints = capBasisPoints
	}
	additionalCents := (int64(currentRentCents) * int64(rateBasisPoints)) / 10000
	return currentRentCents + int(additionalCents)
}

func withinOfferWindow(nowDay, endDay, minDays, maxDays int) bool {
	daysBeforeEnd := endDay - nowDay
	return daysBeforeEnd >= minDays && daysBeforeEnd <= maxDays
}

func responseOnTime(nowDay, issuedAt, responseDays int) bool {
	return nowDay >= issuedAt && nowDay <= issuedAt+responseDays
}

func strictlyBetweenRent(rentCents, currentRentCents, offeredRentCents int) bool {
	low := currentRentCents
	high := offeredRentCents
	if low > high {
		low, high = high, low
	}
	return rentCents > low && rentCents < high
}
