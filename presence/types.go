package presence

// Status is the visible or real presence state of a user or device.
// Numeric order follows aggregation priority: Online > Busy > Away > Offline.
type Status int

const (
	Offline Status = iota
	Away
	Busy
	Online
)

func (s Status) String() string {
	switch s {
	case Online:
		return "online"
	case Busy:
		return "busy"
	case Away:
		return "away"
	default:
		return "offline"
	}
}

// DeviceInfo is the current state of a single device of the queried user.
type DeviceInfo struct {
	Device string
	Status Status
	Expiry int64
}

// QueryResult is returned by Query. Devices is populated only for self queries.
type QueryResult struct {
	Status       Status
	ActiveDevice int
	Devices      []DeviceInfo
}

// Notification is one visible-state change delivered to a subscriber.
type Notification struct {
	Target    string
	Status    Status
	Effective int64
	// switchEvent is false for a lease-expiry event and true for a
	// visibility switch (report/offline/invisible/block/unblock). At an equal
	// effective instant expiry events sort before switch events.
	switchEvent bool
	Seq         uint64
}

// DrainResult is returned by Drain.
type DrainResult struct {
	Notifications []Notification
	Dropped       int
}
