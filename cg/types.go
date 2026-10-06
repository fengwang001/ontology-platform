package cg

// Phase of a snapshot lifecycle: the group is idle, or a snapshot is
// freezing / frozen / ended.
type Phase int

const (
	// Idle means no snapshot is in progress for the group.
	Idle Phase = iota
	// Freezing: snapshot started, waiting for per-volume freeze confirmations.
	Freezing
	// Frozen: every volume confirmed; the snapshot point is fixed.
	Frozen
	// Ended: the snapshot was committed or aborted (including auto-abort).
	Ended
)

func (p Phase) String() string {
	switch p {
	case Freezing:
		return "freezing"
	case Frozen:
		return "frozen"
	case Ended:
		return "ended"
	default:
		return "idle"
	}
}

// Config configures a Coordinator.
type Config struct {
	// DefaultQueueCapacity is the freeze queue capacity used for volumes
	// that do not specify an override at group creation.
	DefaultQueueCapacity int
	// MaxFreezeHold is the maximum allowed duration between the snapshot
	// point and commit while frozen. Commit exactly at the boundary is valid.
	MaxFreezeHold int64
}

// GroupSpec describes a group creation request.
type GroupSpec struct {
	GroupID    string
	VolumeIDs  []string
	Capacities map[string]int
}

// WriteResult is the result of a write operation.
type WriteResult struct {
	// Seq is the assigned write sequence number; zero when the write was
	// queued or discarded.
	Seq uint64
	// Queued reports that the write was enqueued for later application.
	Queued bool
}

// SnapshotRecord is produced by a successful commit.
type SnapshotRecord struct {
	ID      uint64
	GroupID string
	// Point is the instant the last volume confirmed (snapshot point).
	Point int64
	// Deadline is the freeze deadline given at begin.
	Deadline int64
	// Cutoffs maps each volume id to the largest applied write sequence at
	// the snapshot point.
	Cutoffs map[string]uint64
}

// VolumeStatus is a read-only view of a volume.
type VolumeStatus struct {
	ID            string
	Seq           uint64
	QueuedWrites  int
	QueueCapacity int
	GroupID       string
}

// GroupStatus is a read-only view of a group and its snapshot.
type GroupStatus struct {
	ID         string
	Phase      Phase
	Members    []string
	Deadline   int64
	Point      int64
	SnapshotID uint64
	Confirmed  []string
}
