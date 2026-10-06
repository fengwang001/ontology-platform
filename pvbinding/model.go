package pvbinding

func newVolume(spec VolumeSpec) (Volume, error) {
	if !validateName(spec.Name) {
		return Volume{}, bindingError(ErrCodeInvalidArgument, "volume name is required")
	}
	if spec.Capacity <= 0 {
		return Volume{}, bindingError(ErrCodeInvalidArgument, "volume %q capacity must be positive", spec.Name)
	}
	if !validateName(spec.StorageClass) {
		return Volume{}, bindingError(ErrCodeInvalidArgument, "volume %q storage class is required", spec.Name)
	}
	if spec.ReclaimPolicy != ReclaimRetain && spec.ReclaimPolicy != ReclaimDelete {
		return Volume{}, bindingError(ErrCodeInvalidArgument, "volume %q has invalid reclaim policy", spec.Name)
	}
	modes, err := stringSet(spec.AccessModes, "access mode")
	if err != nil {
		return Volume{}, err
	}
	nodes := map[string]struct{}{}
	if spec.NodeNames != nil {
		for _, node := range spec.NodeNames {
			if !validateName(node) {
				return Volume{}, bindingError(ErrCodeInvalidArgument, "volume %q has empty node name", spec.Name)
			}
			nodes[node] = struct{}{}
		}
	}
	if spec.HasReservation && !validateName(spec.ReservationName) {
		return Volume{}, bindingError(ErrCodeInvalidArgument, "volume %q reservation name is required", spec.Name)
	}
	return Volume{
		Name:              spec.Name,
		Capacity:          spec.Capacity,
		StorageClass:      spec.StorageClass,
		AccessModes:       modes,
		Labels:            copyStringMap(spec.Labels),
		NodeNames:         nodes,
		HasNodeConstraint: spec.NodeNames != nil,
		ReservationName:   spec.ReservationName,
		HasReservation:    spec.HasReservation,
		ReclaimPolicy:     spec.ReclaimPolicy,
		State:             VolumeAvailable,
	}, nil
}

func newClaim(spec ClaimSpec) (Claim, error) {
	if !validateName(spec.Name) {
		return Claim{}, bindingError(ErrCodeInvalidArgument, "claim name is required")
	}
	if spec.RequestedCapacity <= 0 {
		return Claim{}, bindingError(ErrCodeInvalidArgument, "claim %q requested capacity must be positive", spec.Name)
	}
	if !validateName(spec.StorageClass) {
		return Claim{}, bindingError(ErrCodeInvalidArgument, "claim %q storage class is required", spec.Name)
	}
	if spec.BindingMode != BindingImmediate && spec.BindingMode != BindingDelayed {
		return Claim{}, bindingError(ErrCodeInvalidArgument, "claim %q has invalid binding mode", spec.Name)
	}
	if spec.HasVolumeName && !validateName(spec.VolumeName) {
		return Claim{}, bindingError(ErrCodeInvalidArgument, "claim %q specified volume name is required", spec.Name)
	}
	modes, err := stringSet(spec.AccessModes, "access mode")
	if err != nil {
		return Claim{}, err
	}
	return Claim{
		Name:              spec.Name,
		RequestedCapacity: spec.RequestedCapacity,
		StorageClass:      spec.StorageClass,
		AccessModes:       modes,
		Selector:          copyStringMap(spec.Selector),
		VolumeName:        spec.VolumeName,
		HasVolumeName:     spec.HasVolumeName,
		BindingMode:       spec.BindingMode,
	}, nil
}

func stringSet(values []string, field string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validateName(value) {
			return nil, bindingError(ErrCodeInvalidArgument, "empty %s", field)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func copyStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
