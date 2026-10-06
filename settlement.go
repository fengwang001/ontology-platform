package congestion

import (
	"sort"
	"strconv"
	"time"
)

func dayKey(at time.Time, loc *time.Location) string {
	return at.In(loc).Format("2006-01-02")
}

func dayBounds(day string, loc *time.Location) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return time.Time{}, time.Time{}, ErrInvalidArgument
	}
	return start, start.AddDate(0, 0, 1), nil
}

func settleDay(
	vehicleID, day string,
	entries []Entry,
	entitlements []Entitlement,
	zones map[string]*zoneNode,
	config Config,
) (payable int64, evidence []string, err error) {
	start, end, err := dayBounds(day, config.Location)
	if err != nil {
		return 0, nil, err
	}

	type registration struct {
		zoneID string
		at     time.Time
		order  int
	}
	registrations := make(map[string]registration)
	dayEntries := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.VehicleID == vehicleID && !entry.At.Before(start) && entry.At.Before(end) {
			dayEntries = append(dayEntries, entry)
		}
	}
	sort.SliceStable(dayEntries, func(i, j int) bool {
		return dayEntries[i].At.Before(dayEntries[j].At)
	})

	for index, entry := range dayEntries {
		zone := zones[entry.ZoneID]
		if zone == nil {
			return 0, nil, ErrZoneNotFound
		}
		zoneIDs := expandZone(entry.ZoneID, zones)
		for _, zoneID := range zoneIDs {
			if _, exists := registrations[zoneID]; exists {
				continue
			}
			if !withinChargeWindow(entry.At, zones[zoneID].config, config.Location) {
				continue
			}
			registrations[zoneID] = registration{zoneID: zoneID, at: entry.At, order: index}
			evidence = append(evidence, "entry "+entry.At.Format(time.RFC3339Nano)+" registered "+zoneID)
		}
	}

	charged := make([]registration, 0, len(registrations))
	for _, registration := range registrations {
		charged = append(charged, registration)
	}
	sort.SliceStable(charged, func(i, j int) bool {
		if charged[i].at.Equal(charged[j].at) {
			return charged[i].order < charged[j].order
		}
		return charged[i].at.Before(charged[j].at)
	})

	disabled := false
	for _, registration := range charged {
		if entitlementApplies(entitlements, Disabled, "", registration.at) {
			disabled = true
			break
		}
	}

	for _, registration := range charged {
		zone := zones[registration.zoneID]
		discounted := zone.config.Fee
		basis := "full fee"
		switch {
		case disabled:
			discounted = 0
			basis = "disabled exemption"
		case entitlementApplies(entitlements, NewEnergy, "", registration.at):
			discounted = 0
			basis = "new energy exemption"
		case entitlementApplies(entitlements, Resident, registration.zoneID, registration.at):
			discounted = residentCharge(zone.config.Fee, entitlements, registration.zoneID, registration.at, config.MinorUnitDivisor)
			basis = "resident discount"
		}

		remaining := config.DailyCap - payable
		applied := discounted
		if applied > remaining {
			applied = remaining
		}
		payable += applied
		evidence = append(evidence, "zone "+registration.zoneID+" fee="+itoa(zone.config.Fee)+
			" discounted="+itoa(discounted)+" applied="+itoa(applied)+" "+basis)
	}

	evidence = append(evidence, "settled payable="+itoa(payable)+" cap="+itoa(config.DailyCap))
	return payable, evidence, nil
}

func withinChargeWindow(at time.Time, zone ZoneConfig, loc *time.Location) bool {
	local := at.In(loc)
	seconds := local.Hour()*3600 + local.Minute()*60 + local.Second()
	start := zone.Start.Hour*3600 + zone.Start.Minute*60
	end := zone.End.Hour*3600 + zone.End.Minute*60
	return seconds >= start && seconds < end
}

func entitlementApplies(entitlements []Entitlement, kind EntitlementKind, zoneID string, at time.Time) bool {
	for _, entitlement := range entitlements {
		if entitlement.Kind != kind || !at.Before(entitlement.End) || at.Before(entitlement.Start) {
			continue
		}
		if kind == Resident && entitlement.ZoneID != zoneID {
			continue
		}
		return true
	}
	return false
}

func residentCharge(gross int64, entitlements []Entitlement, zoneID string, at time.Time, divisor int64) int64 {
	for _, entitlement := range entitlements {
		if entitlement.Kind != Resident || entitlement.ZoneID != zoneID || at.Before(entitlement.Start) || !at.Before(entitlement.End) {
			continue
		}
		raw := gross * (10000 - entitlement.DiscountBasisPts)
		if divisor <= 1 {
			return (raw + 9999) / 10000
		}
		return ((raw+9999)/10000 + divisor - 1) / divisor * divisor
	}
	return gross
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
