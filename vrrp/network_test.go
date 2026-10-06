package vrrp_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/vrrp"
)

func broadcast(t *testing.T, devices map[string]*vrrp.Device, sender string, adverts []vrrp.Advertisement, now uint64) {
	t.Helper()
	for _, advert := range adverts {
		for id, device := range devices {
			if id == sender {
				continue
			}
			if _, err := device.ReceiveAdvertisement(advert, now); err != nil && !errors.Is(err, vrrp.ErrNotStarted) {
				t.Fatalf("receive from %q on %q: %v", sender, id, err)
			}
		}
	}
}

func takeAndBroadcast(t *testing.T, devices map[string]*vrrp.Device, sender string, now uint64) {
	t.Helper()
	result, err := devices[sender].TakeAdvertisements(now)
	if err != nil {
		t.Fatal(err)
	}
	broadcast(t, devices, sender, result.Advertisements, now)
}

func TestMultiDeviceElectionAndFailover(t *testing.T) {
	configs := map[string]vrrp.Config{
		"low":   {ID: "low", Priority: 50, Preempt: true, AdvertIntervalMS: 100},
		"high":  {ID: "high", Priority: 150, Preempt: true, AdvertIntervalMS: 100},
		"owner": {ID: "owner", Priority: 255, AdvertIntervalMS: 100},
	}
	devices := make(map[string]*vrrp.Device, len(configs))
	for id, config := range configs {
		device, err := vrrp.NewDevice(config)
		if err != nil {
			t.Fatal(err)
		}
		devices[id] = device
	}

	result, err := devices["owner"].Start(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Advertisements) != 1 {
		t.Fatalf("owner start advertisements = %d", len(result.Advertisements))
	}
	if _, err := devices["low"].Start(0); err != nil {
		t.Fatal(err)
	}
	if _, err := devices["high"].Start(0); err != nil {
		t.Fatal(err)
	}
	broadcast(t, devices, "owner", result.Advertisements, 0)

	for id, device := range devices {
		want := vrrp.RoleBackup
		if id == "owner" {
			want = vrrp.RoleMaster
		}
		if device.Role() != want {
			t.Fatalf("%s role = %s, want %s", id, roleName(device.Role()), roleName(want))
		}
	}

	takeAndBroadcast(t, devices, "owner", 100)

	stopped, err := devices["owner"].Stop(150)
	if err != nil {
		t.Fatal(err)
	}
	broadcast(t, devices, "owner", stopped.Advertisements, 150)

	for _, id := range []string{"low", "high"} {
		if _, err := devices[id].AdvanceTime(175); err != nil {
			t.Fatal(err)
		}
	}
	takeAndBroadcast(t, devices, "high", 175)

	if devices["high"].Role() != vrrp.RoleMaster || devices["low"].Role() != vrrp.RoleBackup {
		t.Fatalf("roles after shutdown: high=%s low=%s",
			roleName(devices["high"].Role()), roleName(devices["low"].Role()))
	}

	stopped, err = devices["high"].Stop(200)
	if err != nil {
		t.Fatal(err)
	}
	broadcast(t, devices, "high", stopped.Advertisements, 200)
	if _, err := devices["low"].AdvanceTime(225); err != nil {
		t.Fatal(err)
	}
	if devices["low"].Role() != vrrp.RoleMaster {
		t.Fatalf("low role after high stop = %s", roleName(devices["low"].Role()))
	}
}

func TestConcurrentOperationsAreSerializeable(t *testing.T) {
	device, err := vrrp.NewDevice(vrrp.Config{
		ID: "concurrent", Priority: 100, Preempt: true, AdvertIntervalMS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := device.Start(0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < 100; index++ {
				now := uint64(1 + worker*1000 + index)
				_, _ = device.TakeAdvertisements(now)
				_, _ = device.SetPreempt(index%2 == 0, now+1)
			}
		}(worker)
	}
	wg.Wait()

	wg.Wait()
}
