package pvbinding

func validateVolumeSpec(name string, s VolumeSpec) error {
	const op = "AddVolume"
	if name == "" {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "volume name is empty"}
	}
	if s.Capacity <= 0 {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "volume capacity must be positive"}
	}
	if s.StorageClass == "" {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "volume storage class is empty"}
	}
	if len(s.AccessModes) == 0 {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "volume access modes are empty"}
	}
	if s.Reclaim != ReclaimRetain && s.Reclaim != ReclaimDelete {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "unknown reclaim policy"}
	}
	return nil
}

func validateClaimSpec(name string, s ClaimSpec) error {
	const op = "AddClaim"
	if name == "" {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "claim name is empty"}
	}
	if s.RequestCapacity <= 0 {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "request capacity must be positive"}
	}
	if s.StorageClass == "" {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "claim storage class is empty"}
	}
	if len(s.AccessModes) == 0 {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "claim access modes are empty"}
	}
	if s.BindMode != BindImmediate && s.BindMode != BindWaitForConsumer {
		return &ControllerError{Kind: ErrInvalidArgument, Op: op, Detail: "unknown bind mode"}
	}
	return nil
}
