package quorum

import "errors"

// Sentinel errors let callers distinguish every rejection reason.
var (
	// ErrEmptyNodes is returned by New when the initial set is empty.
	ErrEmptyNodes = errors.New("quorum: initial node set is empty")
	// ErrEmptyNodeID is returned when a node identifier is the empty string.
	ErrEmptyNodeID = errors.New("quorum: node identifier must be non-empty")
	// ErrDuplicateNode is returned when a node set contains a repeated identifier.
	ErrDuplicateNode = errors.New("quorum: node set contains a duplicate identifier")

	// ErrUnknownNode is returned by Ack when the node is not currently known.
	ErrUnknownNode = errors.New("quorum: ack from unknown node")

	// ErrNotSingle is returned by BeginJoint when the configuration is already joint.
	ErrNotSingle = errors.New("quorum: BeginJoint requires a single configuration")
	// ErrEmptySet is returned by BeginJoint when newSet is empty.
	ErrEmptySet = errors.New("quorum: new node set must be non-empty")
	// ErrSameSet is returned by BeginJoint when newSet equals the current set.
	ErrSameSet = errors.New("quorum: new node set must differ from the current set")
	// ErrIndexNotAfterCfg is returned by BeginJoint when idx <= cfgIdx.
	ErrIndexNotAfterCfg = errors.New("quorum: joint entry index must be greater than cfgIdx")

	// ErrNotJoint is returned by FinishJoint when the configuration is not joint.
	ErrNotJoint = errors.New("quorum: FinishJoint requires a joint configuration")
	// ErrIndexNotAfterJoint is returned by FinishJoint when idx <= jointIdx.
	ErrIndexNotAfterJoint = errors.New("quorum: new configuration index must be greater than jointIdx")
	// ErrJointNotCommitted is returned by FinishJoint when commit < jointIdx.
	ErrJointNotCommitted = errors.New("quorum: joint configuration entry is not committed yet")
)
