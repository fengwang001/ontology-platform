package pvbinding

import "sort"

type VolumeStatus struct {
	Name              string
	Capacity          int64
	StorageClass      string
	AccessModes       []string
	Labels            map[string]string
	NodeNames         []string
	HasNodeConstraint bool
	ReservationName   string
	HasReservation    bool
	ReclaimPolicy     ReclaimPolicy
	State             VolumeState
}

type ClaimStatus struct {
	Name              string
	RequestedCapacity int64
	StorageClass      string
	AccessModes       []string
	Selector          map[string]string
	VolumeName        string
	HasVolumeName     bool
	BindingMode       BindingMode
	BoundVolumeName   string
}

func (c *Controller) GetVolume(name string) (VolumeStatus, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.volumes[name]
	if !ok {
		return VolumeStatus{}, false
	}
	return volumeStatus(record.volume), true
}

func (c *Controller) GetClaim(name string) (ClaimStatus, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	record, ok := c.claims[name]
	if !ok {
		return ClaimStatus{}, false
	}
	return claimStatus(record.claim), true
}

func volumeStatus(volume Volume) VolumeStatus {
	return VolumeStatus{
		Name:              volume.Name,
		Capacity:          volume.Capacity,
		StorageClass:      volume.StorageClass,
		AccessModes:       sortedSet(volume.AccessModes),
		Labels:            copyStringMap(volume.Labels),
		NodeNames:         sortedSet(volume.NodeNames),
		HasNodeConstraint: volume.HasNodeConstraint,
		ReservationName:   volume.ReservationName,
		HasReservation:    volume.HasReservation,
		ReclaimPolicy:     volume.ReclaimPolicy,
		State:             volume.State,
	}
}

func claimStatus(claim Claim) ClaimStatus {
	return ClaimStatus{
		Name:              claim.Name,
		RequestedCapacity: claim.RequestedCapacity,
		StorageClass:      claim.StorageClass,
		AccessModes:       sortedSet(claim.AccessModes),
		Selector:          copyStringMap(claim.Selector),
		VolumeName:        claim.VolumeName,
		HasVolumeName:     claim.HasVolumeName,
		BindingMode:       claim.BindingMode,
		BoundVolumeName:   claim.BoundVolumeName,
	}
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
