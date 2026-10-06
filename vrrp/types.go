package vrrp

type Role uint8

const (
	RoleInitialize Role = iota
	RoleBackup
	RoleMaster
)

type Config struct {
	ID               string
	Priority         int
	Preempt          bool
	AdvertIntervalMS uint64
}

type Advertisement struct {
	SenderID         string
	Priority         int
	AdvertIntervalMS uint64
}

type EventResult struct {
	Advertisements []Advertisement
}
