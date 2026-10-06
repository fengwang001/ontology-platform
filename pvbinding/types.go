package pvbinding

// AccessMode 是卷与声明共有的访问模式类型。
type AccessMode string

const (
	// ReadWriteOnce 单节点读写。
	ReadWriteOnce AccessMode = "RWO"
	// ReadOnlyMany 多节点只读。
	ReadOnlyMany AccessMode = "ROX"
	// ReadWriteMany 多节点读写。
	ReadWriteMany AccessMode = "RWX"
)

// ReclaimPolicy 卷的回收策略。
type ReclaimPolicy string

const (
	// ReclaimRetain 删除绑定声明后卷停留在 Released。
	ReclaimRetain ReclaimPolicy = "Retain"
	// ReclaimDelete 删除绑定声明后卷随即被移除。
	ReclaimDelete ReclaimPolicy = "Delete"
)

// VolumePhase 卷状态。
type VolumePhase string

const (
	// VolumeAvailable 可用，可参与匹配。
	VolumeAvailable VolumePhase = "Available"
	// VolumeBound 已绑定到某个声明。
	VolumeBound VolumePhase = "Bound"
	// VolumeReleased 曾绑定、声明已删除，仅保留状态，不可再绑定。
	VolumeReleased VolumePhase = "Released"
)

// BindMode 声明的绑定模式。
type BindMode string

const (
	// BindImmediate 立即绑定：提交及每次触发事件时选卷。
	BindImmediate BindMode = "Immediate"
	// BindWaitForConsumer 延迟绑定：仅在联合绑定调用中指派。
	BindWaitForConsumer BindMode = "WaitForConsumer"
)

// VolumeSpec 描述一个持久卷的不可变身份之外的全部属性。
type VolumeSpec struct {
	// Capacity 卷容量，必须为正数。
	Capacity int64
	// StorageClass 存储类名称。
	StorageClass string
	// AccessModes 卷提供的访问模式集合。
	AccessModes map[AccessMode]bool
	// Labels 卷标签集合。
	Labels map[string]string
	// NodeNames 节点约束：为空表示不限；非空时仅这些节点上的消费者可使用。
	NodeNames map[string]bool
	// ReservedClaim 可选的预留声明名称；非空时只有该名称的声明可绑定。
	ReservedClaim string
	// Reclaim 回收策略。
	Reclaim ReclaimPolicy
}

// ClaimSpec 描述一个持久卷声明的请求。
type ClaimSpec struct {
	// RequestCapacity 请求容量，必须为正数。
	RequestCapacity int64
	// StorageClass 所需存储类。
	StorageClass string
	// AccessModes 所需访问模式集合，卷必须全部包含。
	AccessModes map[AccessMode]bool
	// Selector 标签选择器：要求每个键值在卷标签上全部相等；为空表示不限制。
	Selector map[string]string
	// VolumeName 可选的指定卷名称；非空时只能绑定该卷。
	VolumeName string
	// BindMode 立即或延迟。
	BindMode BindMode
}

// Volume 是控制器内部的卷状态视图。
type Volume struct {
	Name  string
	Spec  VolumeSpec
	Phase VolumePhase
	// ClaimName 绑定的声明名称；Available 状态下表示预绑定预留，Bound 状态下为实际绑定。
	ClaimName string
}

// Claim 是控制器内部的声明状态视图。
type Claim struct {
	Name  string
	Spec  ClaimSpec
	Bound bool
	// VolumeName 绑定的卷名称；Bound 为 true 时非空。
	VolumeName string
}

// Snapshot 是某一时刻控制器全量状态的深拷贝。
type Snapshot struct {
	Volumes map[string]*Volume
	Claims  map[string]*Claim
}

// Assignment 是联合绑定中一条声明到卷的指派结果。
type Assignment struct {
	ClaimName  string
	VolumeName string
}
