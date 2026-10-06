package pvbinding

import (
	"errors"
	"sort"
)

type oracleVolume struct {
	spec    VolumeSpec
	state   VolumeState
	deleted bool
}

type oracleClaim struct {
	spec        ClaimSpec
	boundVolume string
}

type oracle struct {
	volumes map[string]*oracleVolume
	claims  map[string]*oracleClaim
}

func newOracle() *oracle {
	return &oracle{
		volumes: make(map[string]*oracleVolume),
		claims:  make(map[string]*oracleClaim),
	}
}

func oracleContains(actual, required []string) bool {
	for _, want := range required {
		found := false
		for _, got := range actual {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func oracleCandidate(volume *oracleVolume, claim *oracleClaim, node string) bool {
	if volume.deleted || volume.state != VolumeAvailable || volume.spec.StorageClass != claim.spec.StorageClass {
		return false
	}
	if claim.spec.HasVolumeName && volume.spec.Name != claim.spec.VolumeName {
		return false
	}
	if volume.spec.Capacity < claim.spec.RequestedCapacity {
		return false
	}
	if !oracleContains(volume.spec.AccessModes, claim.spec.AccessModes) {
		return false
	}
	for key, value := range claim.spec.Selector {
		if volume.spec.Labels[key] != value {
			return false
		}
	}
	if volume.spec.HasReservation && volume.spec.ReservationName != claim.spec.Name {
		return false
	}
	if node != "" && volume.spec.NodeNames != nil && !oracleContains(volume.spec.NodeNames, []string{node}) {
		return false
	}
	return true
}

func (o *oracle) evaluateImmediate() {
	names := make([]string, 0)
	for name, claim := range o.claims {
		if claim.spec.BindingMode == BindingImmediate && claim.boundVolume == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		claim := o.claims[name]
		var best *oracleVolume
		for _, volume := range o.volumes {
			if !oracleCandidate(volume, claim, "") {
				continue
			}
			if best == nil || volume.spec.Capacity < best.spec.Capacity ||
				(volume.spec.Capacity == best.spec.Capacity && volume.spec.Name < best.spec.Name) {
				best = volume
			}
		}
		if best != nil {
			best.state = VolumeBound
			claim.boundVolume = best.spec.Name
		}
	}
}

func (o *oracle) createVolume(spec VolumeSpec) ErrorCode {
	if _, exists := o.volumes[spec.Name]; exists {
		return ErrCodeConflict
	}
	o.volumes[spec.Name] = &oracleVolume{spec: spec, state: VolumeAvailable}
	o.evaluateImmediate()
	return 0
}

func (o *oracle) updateVolume(spec VolumeSpec) ErrorCode {
	volume, ok := o.volumes[spec.Name]
	if !ok || volume.deleted {
		return ErrCodeNotFound
	}
	for _, claim := range o.claims {
		if claim.boundVolume == spec.Name && (spec.Capacity < claim.spec.RequestedCapacity ||
			spec.StorageClass != claim.spec.StorageClass ||
			!oracleContains(spec.AccessModes, claim.spec.AccessModes)) {
			return ErrCodeConflict
		}
	}
	if volume.state == VolumeBound || volume.state == VolumeReleased {
		spec.ReclaimPolicy = volume.spec.ReclaimPolicy
		spec.ReservationName = volume.spec.ReservationName
		spec.HasReservation = volume.spec.HasReservation
	}
	volume.spec = spec
	if volume.state != VolumeReleased {
		o.evaluateImmediate()
	}
	return 0
}

func (o *oracle) createClaim(spec ClaimSpec) ErrorCode {
	if _, exists := o.claims[spec.Name]; exists {
		return ErrCodeConflict
	}
	if spec.HasVolumeName {
		if volume, ok := o.volumes[spec.VolumeName]; !ok || volume.deleted {
			return ErrCodeNotFound
		}
	}
	o.claims[spec.Name] = &oracleClaim{spec: spec}
	if spec.BindingMode == BindingImmediate {
		o.evaluateImmediate()
	}
	return 0
}

func (o *oracle) deleteClaim(name string) ErrorCode {
	claim, ok := o.claims[name]
	if !ok {
		return ErrCodeNotFound
	}
	if claim.boundVolume != "" {
		volume := o.volumes[claim.boundVolume]
		if volume.spec.ReclaimPolicy == ReclaimDelete {
			delete(o.volumes, volume.spec.Name)
		} else {
			volume.state = VolumeReleased
			volume.spec.ReservationName = ""
			volume.spec.HasReservation = false
		}
	}
	delete(o.claims, name)
	return 0
}

func (o *oracle) expand(name string, capacity int64) ErrorCode {
	claim, ok := o.claims[name]
	if !ok {
		return ErrCodeNotFound
	}
	if claim.boundVolume == "" {
		return ErrCodeConflict
	}
	if capacity <= claim.spec.RequestedCapacity {
		return ErrCodeInvalidArgument
	}
	if capacity > o.volumes[claim.boundVolume].spec.Capacity {
		return ErrCodeInsufficientCapacity
	}
	claim.spec.RequestedCapacity = capacity
	return 0
}

func errorCode(err error) ErrorCode {
	var bindingErr Error
	if errors.As(err, &bindingErr) {
		return bindingErr.Code
	}
	return 0
}
