package pvbinding

import (
	"sort"
	"sync"
)

type Controller struct {
	mu      sync.RWMutex
	volumes map[string]*volumeRecord
	claims  map[string]*claimRecord
	byClass map[string]map[string]*volumeRecord
}

type volumeRecord struct {
	volume Volume
}

type claimRecord struct {
	claim Claim
}

func NewController() *Controller {
	return &Controller{
		volumes: make(map[string]*volumeRecord),
		claims:  make(map[string]*claimRecord),
		byClass: make(map[string]map[string]*volumeRecord),
	}
}

func (c *Controller) CreateVolume(spec VolumeSpec) error {
	volume, err := newVolume(spec)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.volumes[spec.Name]; exists {
		return bindingError(ErrCodeConflict, "volume %q already exists", spec.Name)
	}
	record := &volumeRecord{volume: volume}
	c.volumes[volume.Name] = record
	c.classIndex(volume.StorageClass)[volume.Name] = record
	c.evaluatePendingImmediateClaims()
	return c.checkConsistencyLocked()
}

func (c *Controller) UpdateVolume(spec VolumeSpec) error {
	updated, err := newVolume(spec)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.volumes[spec.Name]
	if !ok {
		return bindingError(ErrCodeNotFound, "volume %q not found", spec.Name)
	}
	if boundClaimName := c.boundClaimNameLocked(spec.Name); boundClaimName != "" {
		claim := c.claims[boundClaimName]
		if updated.Capacity < claim.claim.RequestedCapacity ||
			updated.StorageClass != claim.claim.StorageClass ||
			!containsAll(updated.AccessModes, claim.claim.AccessModes) ||
			!matchesLabels(updated.Labels, claim.claim.Selector) {
			return bindingError(ErrCodeConflict, "volume %q would stop satisfying its bound claim", spec.Name)
		}
	}
	oldClass := record.volume.StorageClass
	previousState := record.volume.State
	previousReservationName := record.volume.ReservationName
	previousHasReservation := record.volume.HasReservation
	if previousState == VolumeBound || previousState == VolumeReleased {
		updated.State = previousState
		updated.ReservationName = previousReservationName
		updated.HasReservation = previousHasReservation
	}
	record.volume = updated
	if oldClass != updated.StorageClass {
		delete(c.byClass[oldClass], spec.Name)
		c.classIndex(updated.StorageClass)[spec.Name] = record
	}
	c.evaluatePendingImmediateClaims()
	return c.checkConsistencyLocked()
}

func (c *Controller) ResetVolume(name string) error {
	if !validateName(name) {
		return bindingError(ErrCodeInvalidArgument, "volume name is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.volumes[name]
	if !ok {
		return bindingError(ErrCodeNotFound, "volume %q not found", name)
	}
	if record.volume.State != VolumeReleased {
		return bindingError(ErrCodeConflict, "volume %q is not released", name)
	}
	record.volume.State = VolumeAvailable
	record.volume.ReservationName = ""
	record.volume.HasReservation = false
	c.evaluatePendingImmediateClaims()
	return c.checkConsistencyLocked()
}

func (c *Controller) CreateClaim(spec ClaimSpec) error {
	claim, err := newClaim(spec)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.claims[spec.Name]; exists {
		return bindingError(ErrCodeConflict, "claim %q already exists", spec.Name)
	}
	if claim.HasVolumeName && c.volumes[claim.VolumeName] == nil {
		return bindingError(ErrCodeNotFound, "specified volume %q not found", claim.VolumeName)
	}
	record := &claimRecord{claim: claim}
	c.claims[claim.Name] = record
	if claim.BindingMode == BindingImmediate {
		if volume := c.chooseImmediate(record); volume != nil {
			c.bindLocked(volume, record)
		}
	}
	return c.checkConsistencyLocked()
}

func (c *Controller) BindDelayed(nodeName string, claimNames []string) error {
	if !validateName(nodeName) {
		return bindingError(ErrCodeInvalidArgument, "node name is required")
	}
	if len(claimNames) == 0 {
		return bindingError(ErrCodeInvalidArgument, "claim names are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{})
	for _, name := range claimNames {
		if !validateName(name) {
			return bindingError(ErrCodeInvalidArgument, "claim name is required")
		}
		if _, duplicate := seen[name]; duplicate {
			return bindingError(ErrCodeInvalidArgument, "claim %q appears more than once", name)
		}
		seen[name] = struct{}{}
	}
	claims := make([]*claimRecord, 0, len(claimNames))
	for _, name := range claimNames {
		record, ok := c.claims[name]
		if !ok {
			return bindingError(ErrCodeNotFound, "claim %q not found", name)
		}
		if record.claim.BindingMode != BindingDelayed {
			return bindingError(ErrCodeInvalidArgument, "claim %q does not use delayed binding", name)
		}
		if record.claim.Bound() {
			return bindingError(ErrCodeInvalidArgument, "claim %q is already bound", name)
		}
		claims = append(claims, record)
	}
	assignment, ok := c.assignDelayed(claims, nodeName)
	if !ok {
		return bindingError(ErrCodeNoMatchingVolume, "no joint volume assignment satisfies all claims")
	}
	for claim, volume := range assignment {
		c.bindLocked(volume, claim)
	}
	return c.checkConsistencyLocked()
}

func (c *Controller) DeleteClaim(name string) error {
	if !validateName(name) {
		return bindingError(ErrCodeInvalidArgument, "claim name is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.claims[name]
	if !ok {
		return bindingError(ErrCodeNotFound, "claim %q not found", name)
	}
	if record.claim.Bound() {
		volume := c.volumes[record.claim.BoundVolumeName]
		if volume.volume.ReclaimPolicy == ReclaimDelete {
			delete(c.byClass[volume.volume.StorageClass], volume.volume.Name)
			delete(c.volumes, volume.volume.Name)
		} else {
			volume.volume.State = VolumeReleased
			volume.volume.ReservationName = ""
			volume.volume.HasReservation = false
		}
	}
	delete(c.claims, name)
	return c.checkConsistencyLocked()
}

func (c *Controller) ExpandClaim(name string, newCapacity int64) error {
	if !validateName(name) {
		return bindingError(ErrCodeInvalidArgument, "claim name is required")
	}
	if newCapacity <= 0 {
		return bindingError(ErrCodeInvalidArgument, "new capacity must be positive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.claims[name]
	if !ok {
		return bindingError(ErrCodeNotFound, "claim %q not found", name)
	}
	if !record.claim.Bound() {
		return bindingError(ErrCodeConflict, "claim %q is not bound", name)
	}
	if newCapacity <= record.claim.RequestedCapacity {
		return bindingError(ErrCodeInvalidArgument, "new capacity %d must be greater than current capacity %d", newCapacity, record.claim.RequestedCapacity)
	}
	volume := c.volumes[record.claim.BoundVolumeName]
	if newCapacity > volume.volume.Capacity {
		return bindingError(ErrCodeInsufficientCapacity, "volume %q capacity %d is below requested capacity %d", volume.volume.Name, volume.volume.Capacity, newCapacity)
	}
	record.claim.RequestedCapacity = newCapacity
	return c.checkConsistencyLocked()
}

func (c *Controller) CheckConsistency() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.checkConsistencyLocked()
}

func (c *Controller) classIndex(storageClass string) map[string]*volumeRecord {
	if c.byClass[storageClass] == nil {
		c.byClass[storageClass] = make(map[string]*volumeRecord)
	}
	return c.byClass[storageClass]
}

func (c *Controller) evaluatePendingImmediateClaims() {
	pending := make([]*claimRecord, 0)
	for _, claim := range c.claims {
		if claim.claim.BindingMode == BindingImmediate && !claim.claim.Bound() {
			pending = append(pending, claim)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].claim.Name < pending[j].claim.Name
	})
	for _, claim := range pending {
		if volume := c.chooseImmediate(claim); volume != nil {
			c.bindLocked(volume, claim)
		}
	}
}

func (c *Controller) bindLocked(volume *volumeRecord, claim *claimRecord) {
	volume.volume.State = VolumeBound
	claim.claim.BoundVolumeName = volume.volume.Name
}

func (c *Controller) boundClaimNameLocked(volumeName string) string {
	for _, claim := range c.claims {
		if claim.claim.BoundVolumeName == volumeName {
			return claim.claim.Name
		}
	}
	return ""
}

func (c *Controller) checkConsistencyLocked() error {
	volumeToClaim := make(map[string]string)
	for name, record := range c.volumes {
		volume := &record.volume
		if volume.Name != name {
			return bindingError(ErrCodeConflict, "volume map key %q does not match record", name)
		}
		if c.byClass[volume.StorageClass] == nil || c.byClass[volume.StorageClass][name] != record {
			return bindingError(ErrCodeConflict, "volume %q missing from storage class index", name)
		}
	}
	for className, classVolumes := range c.byClass {
		for name, volume := range classVolumes {
			if volume.volume.StorageClass != className {
				return bindingError(ErrCodeConflict, "volume %q in wrong storage class index", name)
			}
			if c.volumes[name] != volume {
				return bindingError(ErrCodeConflict, "volume %q index record mismatch", name)
			}
		}
	}
	for name, record := range c.claims {
		claim := &record.claim
		if claim.Name != name {
			return bindingError(ErrCodeConflict, "claim map key %q does not match record", name)
		}
		if !claim.Bound() {
			continue
		}
		volume, ok := c.volumes[claim.BoundVolumeName]
		if !ok {
			return bindingError(ErrCodeConflict, "claim %q points to missing volume %q", name, claim.BoundVolumeName)
		}
		if volume.volume.State != VolumeBound {
			return bindingError(ErrCodeConflict, "claim %q points to non-bound volume %q", name, volume.volume.Name)
		}
		if other, exists := volumeToClaim[volume.volume.Name]; exists {
			return bindingError(ErrCodeConflict, "volume %q bound to claims %q and %q", volume.volume.Name, other, name)
		}
		volumeToClaim[volume.volume.Name] = name
	}
	for name, record := range c.volumes {
		if record.volume.State == VolumeBound {
			if _, ok := volumeToClaim[name]; !ok {
				return bindingError(ErrCodeConflict, "bound volume %q has no claim", name)
			}
		}
	}
	return nil
}
