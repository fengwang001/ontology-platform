package parking

func billingMinutes(seconds int64) int64 {
	if seconds <= 0 {
		return 0
	}
	return (seconds + 59) / 60
}

func feeFor(res *Reservation, cfg ZoneConfig) FeeDetail {
	detail := FeeDetail{ReservationID: res.ID}
	switch res.Status {
	case StatusReserved, StatusWaitlisted:
		return detail
	case StatusCanceled:
		detail.NoShow = res.Penalty
	case StatusExpired:
		// Grace expiry releases the slot and intentionally bills nothing.
	case StatusCheckedIn, StatusDeparted:
		detail.Base = billingMinutes(res.End-res.Start) * cfg.BaseRatePerMinute
		if res.NeedCharge {
			detail.Charging = billingMinutes(res.End-res.Start) * cfg.ChargingPerMinute
		}
		if res.Status == StatusDeparted && res.DepartAt > res.End {
			detail.Overtime = billingMinutes(res.DepartAt-res.End) * cfg.OvertimePerMinute
		}
		detail.OccupationPenalty = res.Penalty
		detail.CompensationAward = res.Award
	}
	detail.TotalCharged = detail.Base + detail.Charging + detail.Overtime +
		detail.OccupationPenalty + detail.NoShow - detail.CompensationAward
	if detail.TotalCharged < 0 {
		detail.TotalCharged = 0
	}
	return detail
}
