package congestion

import "math"

type zoneNode struct {
	config ZoneConfig
}

func validateZoneConfig(config ZoneConfig) error {
	if config.ID == "" || config.Fee < 0 {
		return ErrInvalidArgument
	}
	if !validRect(config.Bounds) || !validTimeOfDay(config.Start, false) || !validTimeOfDay(config.End, true) {
		return ErrInvalidArgument
	}
	start := config.Start.Hour*60 + config.Start.Minute
	end := config.End.Hour*60 + config.End.Minute
	if start >= end {
		return ErrInvalidArgument
	}
	return nil
}

func validateZoneNesting(config ZoneConfig, zones map[string]*zoneNode) error {
	var parent *zoneNode
	for _, existing := range zones {
		if !rectsIntersect(config.Bounds, existing.config.Bounds) {
			continue
		}
		if rectContains(existing.config.Bounds, config.Bounds) {
			if rectContains(config.Bounds, existing.config.Bounds) {
				return ErrInvalidNesting
			}
			if parent == nil || rectContains(parent.config.Bounds, existing.config.Bounds) {
				parent = existing
			}
			continue
		}
		if rectContains(config.Bounds, existing.config.Bounds) {
			continue
		}
		return ErrInvalidNesting
	}

	parentID := ""
	if parent != nil {
		parentID = parent.config.ID
	}
	if config.ParentID != parentID {
		return ErrInvalidNesting
	}

	for _, existing := range zones {
		if !rectsIntersect(config.Bounds, existing.config.Bounds) || !rectContains(config.Bounds, existing.config.Bounds) {
			continue
		}
		if existing.config.ParentID == parentID {
			existing.config.ParentID = config.ID
		}
	}
	return nil
}

func expandZone(zoneID string, zones map[string]*zoneNode) []string {
	result := []string{zoneID}
	for current := zoneID; ; {
		zone := zones[current]
		if zone == nil || zone.config.ParentID == "" {
			return result
		}
		current = zone.config.ParentID
		result = append(result, current)
	}
}

func rectsIntersect(a, b Rect) bool {
	return a.MinLat < b.MaxLat && b.MinLat < a.MaxLat &&
		a.MinLng < b.MaxLng && b.MinLng < a.MaxLng
}

func rectContains(outer, inner Rect) bool {
	return outer.MinLat <= inner.MinLat && inner.MaxLat <= outer.MaxLat &&
		outer.MinLng <= inner.MinLng && inner.MaxLng <= outer.MaxLng
}

func validRect(rect Rect) bool {
	values := []float64{rect.MinLat, rect.MinLng, rect.MaxLat, rect.MaxLng}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return rect.MinLat < rect.MaxLat && rect.MinLng < rect.MaxLng &&
		rect.MinLat >= -90 && rect.MaxLat <= 90 &&
		rect.MinLng >= -180 && rect.MaxLng <= 180
}

func validTimeOfDay(value TimeOfDay, allowMidnightEnd bool) bool {
	if value.Minute < 0 || value.Minute > 59 {
		return false
	}
	if allowMidnightEnd && value.Hour == 24 {
		return value.Minute == 0
	}
	return value.Hour >= 0 && value.Hour <= 23
}
