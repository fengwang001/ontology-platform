package dynamicforest

import "errors"

var (
	ErrInvalidArgument = errors.New("dynamicforest: invalid argument")
	ErrEdgeNotFound    = errors.New("dynamicforest: edge not found")
	ErrCapacityFull    = errors.New("dynamicforest: edge capacity full")
	ErrSameNode        = errors.New("dynamicforest: path endpoints are the same node")
	ErrNotConnected    = errors.New("dynamicforest: endpoints are not connected")
)
