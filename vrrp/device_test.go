package vrrp

import (
	"errors"
	"testing"
)

func newTestDevice(t *testing.T, config Config) *Device {
	t.Helper()
	device, err := NewDevice(config)
	if err != nil {
		t.Fatalf("NewDevice() error = %v", err)
	}
	return device
}

func assertRole(t *testing.T, device *Device, want Role) {
	t.Helper()
	if got := device.Role(); got != want {
		t.Fatalf("Role() = %d, want %d", got, want)
	}
}

func assertErrorIs(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func assertAdvertisementCount(t *testing.T, result EventResult, want int) {
	t.Helper()
	if got := len(result.Advertisements); got != want {
		t.Fatalf("advertisement count = %d, want %d: %#v", got, want, result)
	}
}

func TestMonitorTimeoutBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   uint64
		want Role
	}{
		{name: "one millisecond before", at: 299, want: RoleBackup},
		{name: "exact boundary", at: 300, want: RoleMaster},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := newTestDevice(t, Config{ID: "b", Priority: 50, AdvertIntervalMS: 40})
			if _, err := device.Start(0); err != nil {
				t.Fatal(err)
			}
			if _, err := device.ReceiveAdvertisement(Advertisement{
				SenderID: "a", Priority: 100, AdvertIntervalMS: 100,
			}, 0); err != nil {
				t.Fatal(err)
			}

			if _, err := device.AdvanceTime(tc.at); err != nil {
				t.Fatal(err)
			}
			assertRole(t, device, tc.want)

			if tc.want == RoleMaster {
				got, err := device.TakeAdvertisements(tc.at)
				if err != nil {
					t.Fatal(err)
				}
				assertAdvertisementCount(t, got, 1)
				if got.Advertisements[0].Priority != 50 {
					t.Fatalf("priority = %d, want 50", got.Advertisements[0].Priority)
				}
			}
		})
	}
}

func TestEqualPriorityUsesSenderID(t *testing.T) {
	device := newTestDevice(t, Config{ID: "b", Priority: 100, AdvertIntervalMS: 100, Preempt: true})
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}

	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "a", Priority: 100, AdvertIntervalMS: 100,
	}, 0); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(300); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleMaster)

	_, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "b", Priority: 100, AdvertIntervalMS: 100,
	}, 300)
	assertErrorIs(t, err, ErrIdentityConflict)
	assertRole(t, device, RoleMaster)

	if _, err := device.Stop(300); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Start(300); err != nil {
		t.Fatal(err)
	}
	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "c", Priority: 100, AdvertIntervalMS: 100,
	}, 300); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
}

func TestPreemptionModesForLowerPriorityAdvertisement(t *testing.T) {
	for _, preempt := range []bool{false, true} {
		t.Run("preempt-"+boolName(preempt), func(t *testing.T) {
			device := newTestDevice(t, Config{
				ID: "local", Priority: 100, AdvertIntervalMS: 10, Preempt: preempt,
			})
			if _, err := device.Start(0); err != nil {
				t.Fatal(err)
			}
			if _, err := device.ReceiveAdvertisement(Advertisement{
				SenderID: "high", Priority: 200, AdvertIntervalMS: 100,
			}, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := device.ReceiveAdvertisement(Advertisement{
				SenderID: "low", Priority: 50, AdvertIntervalMS: 200,
			}, 100); err != nil {
				t.Fatal(err)
			}
			if _, err := device.AdvanceTime(300); err != nil {
				t.Fatal(err)
			}
			want := RoleMaster
			if !preempt {
				want = RoleBackup
			}
			assertRole(t, device, want)
		})
	}
}

func boolName(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestPriorityZeroWaitAndCancellation(t *testing.T) {
	device := newTestDevice(t, Config{ID: "backup", Priority: 100, AdvertIntervalMS: 10})
	if _, err := device.Start(100); err != nil {
		t.Fatal(err)
	}

	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "master", Priority: 0, AdvertIntervalMS: 100,
	}, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := device.AdvanceTime(124); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)

	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "master", Priority: 200, AdvertIntervalMS: 100,
	}, 124); err != nil {
		t.Fatal(err)
	}
	if _, err := device.AdvanceTime(125); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(423); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(424); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleMaster)
}

func TestShutdownWaitHasPriorityWhenTimersTie(t *testing.T) {
	device := newTestDevice(t, Config{ID: "backup", Priority: 100, AdvertIntervalMS: 40})
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}
	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "master", Priority: 200, AdvertIntervalMS: 100,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "master", Priority: 0, AdvertIntervalMS: 1200,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := device.AdvanceTime(299); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(300); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleMaster)
}

func TestAddressOwnerBranches(t *testing.T) {
	device := newTestDevice(t, Config{ID: "owner", Priority: 255, AdvertIntervalMS: 100})
	started, err := device.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, started, 1)
	assertRole(t, device, RoleMaster)

	probe := Advertisement{SenderID: "other", Priority: 0, AdvertIntervalMS: 100}
	got, err := device.ReceiveAdvertisement(probe, 50)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 0)
	got, err = device.TakeAdvertisements(100)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 1)

	_, err = device.SetPriority(254, 101)
	assertErrorIs(t, err, ErrInvalidArgument)
	_, err = device.ReceiveAdvertisement(Advertisement{
		SenderID: "other-owner", Priority: 255, AdvertIntervalMS: 100,
	}, 102)
	assertErrorIs(t, err, ErrIdentityConflict)
	assertRole(t, device, RoleMaster)
}

func TestNonOwnerPriorityZeroProbe(t *testing.T) {
	device := newTestDevice(t, Config{ID: "master", Priority: 100, AdvertIntervalMS: 100})
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}
	if _, err := device.AdvanceTime(300); err != nil {
		t.Fatal(err)
	}

	got, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "backup", Priority: 0, AdvertIntervalMS: 40,
	}, 300)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 1)
	if got.Advertisements[0].SenderID != "master" {
		t.Fatalf("sender = %q", got.Advertisements[0].SenderID)
	}

	empty, err := device.TakeAdvertisements(301)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, empty, 0)
	due, err := device.TakeAdvertisements(400)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, due, 1)
}

func TestMasterDemotionByAdvertisement(t *testing.T) {
	device := newTestDevice(t, Config{ID: "a", Priority: 100, AdvertIntervalMS: 50})
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}
	if _, err := device.AdvanceTime(100); err != nil {
		t.Fatal(err)
	}

	if _, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "b", Priority: 100, AdvertIntervalMS: 100,
	}, 100); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(399); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleBackup)
	if _, err := device.AdvanceTime(400); err != nil {
		t.Fatal(err)
	}
	assertRole(t, device, RoleMaster)
}

func TestRejectionOrderAndRejectedEventsHaveNoEffect(t *testing.T) {
	device := newTestDevice(t, Config{ID: "device", Priority: 100, AdvertIntervalMS: 100})

	_, err := device.ReceiveAdvertisement(Advertisement{
		SenderID: "", Priority: 200, AdvertIntervalMS: 100,
	}, 10)
	assertErrorIs(t, err, ErrInvalidArgument)

	_, err = device.ReceiveAdvertisement(Advertisement{
		SenderID: "peer", Priority: 200, AdvertIntervalMS: 100,
	}, 9)
	assertErrorIs(t, err, ErrNotStarted)

	if _, err := device.AdvanceTime(10); err != nil {
		t.Fatal(err)
	}
	_, err = device.ReceiveAdvertisement(Advertisement{
		SenderID: "peer", Priority: 200, AdvertIntervalMS: 100,
	}, 9)
	assertErrorIs(t, err, ErrClockRegression)

	if _, err := device.Start(10); err != nil {
		t.Fatal(err)
	}
	_, err = device.ReceiveAdvertisement(Advertisement{
		SenderID: "device", Priority: 100, AdvertIntervalMS: 100,
	}, 11)
	assertErrorIs(t, err, ErrIdentityConflict)
	assertRole(t, device, RoleBackup)

	_, err = device.SetPriority(0, 10)
	assertErrorIs(t, err, ErrInvalidArgument)
}

func TestStopAndRestart(t *testing.T) {
	device := newTestDevice(t, Config{ID: "owner", Priority: 255, AdvertIntervalMS: 100})
	started, err := device.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, started, 1)

	stopped, err := device.Stop(200)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, stopped, 1)
	if stopped.Advertisements[0].Priority != 0 {
		t.Fatalf("stop priority = %d, want 0", stopped.Advertisements[0].Priority)
	}
	assertRole(t, device, RoleInitialize)

	restarted, err := device.Start(201)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, restarted, 1)
	assertRole(t, device, RoleMaster)
}

func TestPeriodicAdvertisementsDoNotBacklog(t *testing.T) {
	device := newTestDevice(t, Config{ID: "owner", Priority: 255, AdvertIntervalMS: 100})
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}
	got, err := device.TakeAdvertisements(500)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 1)

	got, err = device.TakeAdvertisements(501)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 0)
	got, err = device.TakeAdvertisements(600)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvertisementCount(t, got, 1)
}
