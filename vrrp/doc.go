// Package vrrp implements a deterministic VRRP-style single-device state
// machine.
//
// The caller supplies a monotonic millisecond clock, received advertisements,
// and operational events. Devices never perform I/O or start goroutines; each
// event returns the advertisements that should be sent immediately. A caller
// can connect multiple devices by passing one device's returned advertisements
// to ReceiveAdvertisement on the other devices.
package vrrp
