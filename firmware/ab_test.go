package firmware

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

type naiveSlot struct {
	version    int
	status     SlotStatus
	trialsLeft int
}

type naiveManager struct {
	slots         [2]naiveSlot
	activeSlot    int
	floor         int
	trialBoots    int
	lastBootTrial bool
}

func newNaiveManager(initialVersion int, trialBoots int) (*naiveManager, error) {
	if initialVersion < 1 {
		return nil, ErrInvalidInitialVersion
	}
	if trialBoots < 1 {
		return nil, ErrInvalidTrialBoots
	}
	return &naiveManager{
		slots: [2]naiveSlot{
			{version: initialVersion, status: StatusGood},
			{version: 0, status: StatusEmpty},
		},
		activeSlot: 0,
		floor:      initialVersion,
		trialBoots: trialBoots,
	}, nil
}

func (n *naiveManager) install(version int) error {
	inactive := 1 - n.activeSlot
	if version == 0 {
		return ErrInvalidInstallVersion
	}
	if version <= n.floor {
		return ErrVersionNotAboveFloor
	}
	if n.slots[inactive].status == StatusTrial {
		return ErrInactiveSlotOnTrial
	}
	n.slots[inactive] = naiveSlot{version: version, status: StatusTrial, trialsLeft: n.trialBoots}
	return nil
}

func (n *naiveManager) boot() BootResult {
	inactive := 1 - n.activeSlot
	if n.slots[inactive].status == StatusTrial {
		if n.slots[inactive].trialsLeft > 0 {
			n.slots[inactive].trialsLeft--
			n.lastBootTrial = true
			return BootResult{Slot: inactive, Version: n.slots[inactive].version, Trial: true}
		}
		n.slots[inactive].status = StatusBad
		n.slots[inactive].trialsLeft = 0
		n.lastBootTrial = false
		return BootResult{Slot: n.activeSlot, Version: n.slots[n.activeSlot].version, Rollback: true}
	}
	n.lastBootTrial = false
	return BootResult{Slot: n.activeSlot, Version: n.slots[n.activeSlot].version}
}

func (n *naiveManager) confirm() error {
	if !n.lastBootTrial {
		return ErrNoTrialToConfirm
	}
	trialSlot := 1 - n.activeSlot
	n.slots[trialSlot].status = StatusGood
	n.slots[trialSlot].trialsLeft = 0
	n.floor = n.slots[trialSlot].version
	n.activeSlot = trialSlot
	n.lastBootTrial = false
	return nil
}

func (n *naiveManager) state() State {
	return State{
		Slots: [2]Slot{
			{Version: n.slots[0].version, Status: n.slots[0].status, TrialsLeft: n.slots[0].trialsLeft},
			{Version: n.slots[1].version, Status: n.slots[1].status, TrialsLeft: n.slots[1].trialsLeft},
		},
		ActiveSlot:    n.activeSlot,
		Floor:         n.floor,
		TrialBoots:    n.trialBoots,
		LastBootTrial: n.lastBootTrial,
	}
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		initial int
		m       int
		want    error
		reason  string
	}{
		{0, 3, ErrInvalidInitialVersion, "initial version is checked before M"},
		{-1, 0, ErrInvalidInitialVersion, "invalid initial version remains the first error"},
		{1, 0, ErrInvalidTrialBoots, "initial version is valid and M is checked next"},
		{1, -2, ErrInvalidTrialBoots, "trial count must be at least one"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			manager, err := NewManager(tc.initial, tc.m)
			if !errors.Is(err, tc.want) || manager != nil {
				t.Fatalf("NewManager(%d,%d)=(%v,%v), want error %v with nil manager", tc.initial, tc.m, manager, err, tc.want)
			}
			t.Logf("input=NewManager(initial=%d,M=%d) output=%s decision=%s", tc.initial, tc.m, errorText(err), tc.reason)
		})
	}
}

func TestRulesAgainstNaiveSimulation(t *testing.T) {
	for _, m := range []int{1, 3} {
		t.Run(fmt.Sprintf("M=%d", m), func(t *testing.T) {
			manager, err := NewManager(10, m)
			if err != nil {
				t.Fatal(err)
			}
			naive, err := newNaiveManager(10, m)
			if err != nil {
				t.Fatal(err)
			}
			compareState(t, manager, naive, "NewManager")
			t.Logf("input=NewManager(initial=10,M=%d) output=%+v decision=slot0 good/active, slot1 empty, floor=10", m, manager.Snapshot())

			install(t, manager, naive, 10, ErrVersionNotAboveFloor, "equal to floor is rejected")
			install(t, manager, naive, 0, ErrInvalidInstallVersion, "zero is rejected before floor comparison")
			install(t, manager, naive, 9, ErrVersionNotAboveFloor, "below floor is rejected")
			install(t, manager, naive, 11, nil, "floor+1 is installed into empty inactive slot")
			install(t, manager, naive, 12, ErrInactiveSlotOnTrial, "Trial inactive slot cannot be replaced")

			for bootNumber := 1; bootNumber <= m; bootNumber++ {
				result := runBoot(t, manager, naive, fmt.Sprintf("boot %d: trials remain, so consume one", bootNumber))
				if result != (BootResult{Slot: 1, Version: 11, Trial: true}) {
					t.Fatalf("trial boot %d = %+v", bootNumber, result)
				}
			}

			rollback := runBoot(t, manager, naive, "M trial boots are exhausted; mark candidate Bad and boot active slot")
			if rollback != (BootResult{Slot: 0, Version: 10, Rollback: true}) {
				t.Fatalf("rollback = %+v", rollback)
			}

			confirm(t, manager, naive, ErrNoTrialToConfirm, "last boot was rollback, not trial")
			if got := manager.Snapshot().Floor; got != 10 {
				t.Fatalf("floor after rollback = %d, want 10", got)
			}

			install(t, manager, naive, 11, nil, "Bad slot can be overwritten because floor is still 10")
			runBoot(t, manager, naive, "newly installed candidate restarts at M trial boots")
			confirm(t, manager, naive, nil, "trial is confirmed; slot1 becomes good/active and floor rises to 11")
			confirm(t, manager, naive, ErrNoTrialToConfirm, "confirmation clears the last-trial marker")
			install(t, manager, naive, 11, ErrVersionNotAboveFloor, "same confirmed version now equals the new floor")
			install(t, manager, naive, 12, nil, "former active slot remains Good but is inactive and may be overwritten")

			final := manager.Snapshot()
			if final.ActiveSlot != 1 || final.Floor != 11 {
				t.Fatalf("final state = %+v, want active slot1 floor11", final)
			}
			if final.Slots[0] != (Slot{Version: 12, Status: StatusTrial, TrialsLeft: m}) {
				t.Fatalf("slot0 = %+v", final.Slots[0])
			}
		})
	}
}

func TestRejectedConfirmIsAtomicAfterNormalBoot(t *testing.T) {
	manager, err := NewManager(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := manager.Snapshot()
	result := manager.Boot()
	if result != (BootResult{Slot: 0, Version: 2}) {
		t.Fatalf("normal boot = %+v", result)
	}
	if err := manager.Confirm(); !errors.Is(err, ErrNoTrialToConfirm) {
		t.Fatalf("Confirm error = %v", err)
	}
	after := manager.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected Confirm changed state: before=%+v after=%+v", before, after)
	}
	t.Logf("input=Boot then Confirm output=boot=%+v error=%s decision=normal boot leaves no pending confirmation", result, errorText(err))
}

func TestConcurrentCalls(t *testing.T) {
	manager, err := NewManager(500, 2)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(1)
	done.Add(workers * 3)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer done.Done()
			ready.Wait()
			_ = manager.Install(501 + i)
		}()
		go func() {
			defer done.Done()
			ready.Wait()
			_ = manager.Boot()
		}()
		go func() {
			defer done.Done()
			ready.Wait()
			_ = manager.Confirm()
		}()
	}
	ready.Done()
	done.Wait()

	got := manager.Snapshot()
	if got.Slots[got.ActiveSlot].Status != StatusGood {
		t.Fatalf("active slot is %+v, want Good", got.Slots[got.ActiveSlot])
	}
	if got.Floor != got.Slots[got.ActiveSlot].Version || got.Floor < 500 {
		t.Fatalf("floor/active version invariant failed: %+v", got)
	}
	for i, slot := range got.Slots {
		if slot.Status == StatusTrial && slot.Version <= got.Floor {
			t.Fatalf("trial slot%d version=%d floor=%d", i, slot.Version, got.Floor)
		}
	}
	t.Logf("input=%d each Install/Boot/Confirm concurrent calls output=%+v decision=serial-equivalent invariants hold", workers, got)
}

func install(t *testing.T, manager *Manager, naive *naiveManager, version int, want error, reason string) {
	t.Helper()
	before := manager.Snapshot()
	err := manager.Install(version)
	naiveErr := naive.install(version)
	if !errors.Is(err, want) || !errors.Is(naiveErr, want) {
		t.Fatalf("Install(%d) actual=%v naive=%v want=%v", version, err, naiveErr, want)
	}
	if want != nil && !reflect.DeepEqual(before, manager.Snapshot()) {
		t.Fatalf("rejected Install(%d) changed state before=%+v after=%+v", version, before, manager.Snapshot())
	}
	compareState(t, manager, naive, "Install")
	t.Logf("input=Install(v=%d) output=%s decision=%s state=%+v", version, errorText(err), reason, manager.Snapshot())
}

func runBoot(t *testing.T, manager *Manager, naive *naiveManager, reason string) BootResult {
	t.Helper()
	result := manager.Boot()
	naiveResult := naive.boot()
	if result != naiveResult {
		t.Fatalf("Boot actual=%+v naive=%+v", result, naiveResult)
	}
	compareState(t, manager, naive, "Boot")
	t.Logf("input=Boot output=%+v decision=%s state=%+v", result, reason, manager.Snapshot())
	return result
}

func confirm(t *testing.T, manager *Manager, naive *naiveManager, want error, reason string) {
	t.Helper()
	before := manager.Snapshot()
	err := manager.Confirm()
	naiveErr := naive.confirm()
	if !errors.Is(err, want) || !errors.Is(naiveErr, want) {
		t.Fatalf("Confirm actual=%v naive=%v want=%v", err, naiveErr, want)
	}
	if want != nil && !reflect.DeepEqual(before, manager.Snapshot()) {
		t.Fatalf("rejected Confirm changed state before=%+v after=%+v", before, manager.Snapshot())
	}
	compareState(t, manager, naive, "Confirm")
	t.Logf("input=Confirm output=%s decision=%s state=%+v", errorText(err), reason, manager.Snapshot())
}

func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", err)
}

func compareState(t *testing.T, manager *Manager, naive *naiveManager, operation string) {
	t.Helper()
	got := manager.Snapshot()
	model := naive.state()
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("%s state actual=%+v naive=%+v", operation, got, model)
	}
}
