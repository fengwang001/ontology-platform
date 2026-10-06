package pvbinding

type VolumeState string

const (
	VolumeAvailable VolumeState = "Available"
	VolumeBound     VolumeState = "Bound"
	VolumeReleased  VolumeState = "Released"
)

type ReclaimPolicy string

const (
	ReclaimRetain ReclaimPolicy = "Retain"
	ReclaimDelete ReclaimPolicy = "Delete"
)

type BindingMode string

const (
	BindingImmediate BindingMode = "Immediate"
	BindingDelayed   BindingMode = "Delayed"
)

type VolumeSpec struct {
	Name            string
	Capacity        int64
	StorageClass    string
	AccessModes     []string
	Labels          map[string]string
	NodeNames       []string
	ReservationName string
	HasReservation  bool
	ReclaimPolicy   ReclaimPolicy
}

type Volume struct {
	Name              string
	Capacity          int64
	StorageClass      string
	AccessModes       map[string]struct{}
	Labels            map[string]string
	NodeNames         map[string]struct{}
	HasNodeConstraint bool
	ReservationName   string
	HasReservation    bool
	ReclaimPolicy     ReclaimPolicy
	State             VolumeState
}

type ClaimSpec struct {
	Name              string
	RequestedCapacity int64
	StorageClass      string
	AccessModes       []string
	Selector          map[string]string
	VolumeName        string
	HasVolumeName     bool
	BindingMode       BindingMode
}

type Claim struct {
	Name              string
	RequestedCapacity int64
	StorageClass      string
	AccessModes       map[string]struct{}
	Selector          map[string]string
	VolumeName        string
	HasVolumeName     bool
	BindingMode       BindingMode
	BoundVolumeName   string
}

func (c *Claim) Bound() bool {
	return c.BoundVolumeName != ""
}
