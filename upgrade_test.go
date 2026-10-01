package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type opKind int

const (
	opInstall opKind = iota
	opBoot
	opConfirm
)

type testOp struct {
	kind    opKind
	version int
}

type naiveSlot struct {
	status         SlotStatus
	version        int
	triesRemaining int
}

type naiveManager struct {
	slots            [2]naiveSlot
	activeSlot       int
	floor            int
	maxTrialBoots    int
	lastBootWasTrial bool
	lastTrialSlot    int
}

func newNaiveManager(initialVersion int, maxTrialBoots int) *naiveManager {
	return &naiveManager{
		slots: [2]naiveSlot{
			{status: SlotGood, version: initialVersion},
			{status: SlotEmpty, version: 0},
		},
		floor:         initialVersion,
		maxTrialBoots: maxTrialBoots,
		lastTrialSlot: -1,
	}
}

func (m *naiveManager) snapshot() Snapshot {
	slots := [2]SlotState{
		{Status: m.slots[0].status, Version: m.slots[0].version, TriesRemaining: m.slots[0].triesRemaining},
		{Status: m.slots[1].status, Version: m.slots[1].version, TriesRemaining: m.slots[1].triesRemaining},
	}
	return Snapshot{
		Slots:            slots,
		ActiveSlot:       m.activeSlot,
		Floor:            m.floor,
		LastBootWasTrial: m.lastBootWasTrial,
	}
}

func (m *naiveManager) install(version int) error {
	if version == 0 {
		return ErrInvalidVersion
	}
	if version <= m.floor {
		return ErrVersionNotRaised
	}

	inactiveSlot := 1 - m.activeSlot
	if m.slots[inactiveSlot].status == SlotTrial {
		return ErrInactiveSlotOnTrial
	}

	m.slots[inactiveSlot] = naiveSlot{
		status:         SlotTrial,
		version:        version,
		triesRemaining: m.maxTrialBoots,
	}
	return nil
}

func (m *naiveManager) boot() BootResult {
	inactiveSlot := 1 - m.activeSlot
	if m.slots[inactiveSlot].status != SlotTrial {
		m.lastBootWasTrial = false
		m.lastTrialSlot = -1
		return BootResult{Slot: m.activeSlot, Version: m.slots[m.activeSlot].version}
	}

	if m.slots[inactiveSlot].triesRemaining > 0 {
		m.slots[inactiveSlot].triesRemaining--
		m.lastBootWasTrial = true
		m.lastTrialSlot = inactiveSlot
		return BootResult{
			Slot:    inactiveSlot,
			Version: m.slots[inactiveSlot].version,
			Trial:   true,
		}
	}

	m.slots[inactiveSlot].status = SlotBad
	m.lastBootWasTrial = false
	m.lastTrialSlot = -1
	return BootResult{
		Slot:     m.activeSlot,
		Version:  m.slots[m.activeSlot].version,
		Rollback: true,
	}
}

func (m *naiveManager) confirm() error {
	if !m.lastBootWasTrial {
		return ErrNoTrialBoot
	}

	trialSlot := m.lastTrialSlot
	m.slots[trialSlot].status = SlotGood
	m.slots[trialSlot].triesRemaining = 0
	m.activeSlot = trialSlot
	m.floor = m.slots[trialSlot].version
	m.lastBootWasTrial = false
	m.lastTrialSlot = -1
	return nil
}

type observation struct {
	before   Snapshot
	after    Snapshot
	output   any
	expected any
	decision string
}

func TestRulesAgainstNaiveSimulation(t *testing.T) {
	for _, maxTrialBoots := range []int{1, 3} {
		t.Run(fmt.Sprintf("M%d", maxTrialBoots), func(t *testing.T) {
			manager, err := NewUpgradeManager(1, maxTrialBoots)
			if err != nil {
				t.Fatalf("NewUpgradeManager() error = %v", err)
			}
			reference := newNaiveManager(1, maxTrialBoots)
			ops := buildScenario(maxTrialBoots)
			history := make([]observation, len(ops))

			for index, operation := range ops {
				before := manager.Snapshot()
				current := observation{before: before}

				switch operation.kind {
				case opInstall:
					current.decision = installDecision(before, operation.version, maxTrialBoots)
					t.Logf("input[%d]=Install(v=%d); before=%s; decision=%s", index, operation.version, formatSnapshot(before), current.decision)
					current.output = manager.Install(operation.version)
					current.expected = reference.install(operation.version)
				case opBoot:
					current.decision = bootDecision(before)
					t.Logf("input[%d]=Boot; before=%s; decision=%s", index, formatSnapshot(before), current.decision)
					current.output = manager.Boot()
					current.expected = reference.boot()
				case opConfirm:
					current.decision = confirmDecision(before)
					t.Logf("input[%d]=Confirm; before=%s; decision=%s", index, formatSnapshot(before), current.decision)
					current.output = manager.Confirm()
					current.expected = reference.confirm()
				}

				current.after = manager.Snapshot()
				expectedAfter := reference.snapshot()
				t.Logf("output[%d]=%v; after=%s", index, current.output, formatSnapshot(current.after))
				if !sameOutput(current.output, current.expected) {
					t.Fatalf("operation %d output = %v, want %v", index, current.output, current.expected)
				}
				if current.after != expectedAfter {
					t.Fatalf("operation %d state = %s, want %s", index, formatSnapshot(current.after), formatSnapshot(expectedAfter))
				}
				assertInvariants(t, index, current.after)
				history[index] = current
			}

			assertReplay(t, maxTrialBoots, ops, history)
		})
	}
}

func buildScenario(maxTrialBoots int) []testOp {
	operations := []testOp{
		{kind: opConfirm},
		{kind: opInstall, version: 0},
		{kind: opInstall, version: 1},
		{kind: opInstall, version: 2},
		{kind: opInstall, version: 3},
	}

	for bootNumber := 1; bootNumber <= maxTrialBoots; bootNumber++ {
		operations = append(operations, testOp{kind: opBoot})
		if bootNumber == 1 {
			operations = append(operations, testOp{kind: opInstall, version: 3})
		}
	}
	operations = append(operations,
		testOp{kind: opBoot},
		testOp{kind: opConfirm},
		testOp{kind: opInstall, version: 2},
	)

	for bootNumber := 1; bootNumber <= maxTrialBoots; bootNumber++ {
		operations = append(operations, testOp{kind: opBoot})
	}
	operations = append(operations,
		testOp{kind: opConfirm},
		testOp{kind: opConfirm},
		testOp{kind: opInstall, version: 2},
		testOp{kind: opInstall, version: 4},
	)

	for bootNumber := 1; bootNumber <= maxTrialBoots; bootNumber++ {
		operations = append(operations, testOp{kind: opBoot})
	}
	operations = append(operations, testOp{kind: opConfirm})

	return operations
}

func assertReplay(t *testing.T, maxTrialBoots int, operations []testOp, history []observation) {
	t.Helper()
	manager, err := NewUpgradeManager(1, maxTrialBoots)
	if err != nil {
		t.Fatalf("replay NewUpgradeManager() error = %v", err)
	}

	for index, operation := range operations {
		var output any
		switch operation.kind {
		case opInstall:
			output = manager.Install(operation.version)
		case opBoot:
			output = manager.Boot()
		case opConfirm:
			output = manager.Confirm()
		}
		after := manager.Snapshot()
		if !sameOutput(output, history[index].output) || after != history[index].after {
			t.Fatalf("replay mismatch at %d: output=%v state=%s; want output=%v state=%s",
				index, output, formatSnapshot(after), history[index].output, formatSnapshot(history[index].after))
		}
	}
}

func installDecision(before Snapshot, version int, maxTrialBoots int) string {
	inactiveSlot := 1 - before.ActiveSlot
	switch {
	case version == 0:
		return "version zero is rejected and state is unchanged"
	case version <= before.Floor:
		return "version must be strictly greater than floor and state is unchanged"
	case before.Slots[inactiveSlot].Status == SlotTrial:
		return "inactive slot is Trial and cannot be overwritten"
	default:
		return fmt.Sprintf("inactive slot %d receives version %d with %d fresh trial boots", inactiveSlot, version, maxTrialBoots)
	}
}

func bootDecision(before Snapshot) string {
	inactiveSlot := 1 - before.ActiveSlot
	inactive := before.Slots[inactiveSlot]
	switch {
	case inactive.Status != SlotTrial:
		return "inactive slot is not Trial, so active Good slot boots normally"
	case inactive.TriesRemaining > 0:
		return fmt.Sprintf("Trial slot has %d tries, so consume one and trial boot slot %d", inactive.TriesRemaining, inactiveSlot)
	default:
		return "Trial slot has no tries, so mark it Bad and roll back to active Good slot"
	}
}

func confirmDecision(before Snapshot) string {
	if before.LastBootWasTrial {
		return "last boot was Trial, so promote that slot, activate it, and raise floor"
	}
	return "last boot was not Trial, so Confirm is rejected"
}

func formatSnapshot(snapshot Snapshot) string {
	return fmt.Sprintf("{slots:[%s %s] active:%d floor:%d lastTrial:%t}",
		formatSlot(snapshot.Slots[0]), formatSlot(snapshot.Slots[1]),
		snapshot.ActiveSlot, snapshot.Floor, snapshot.LastBootWasTrial)
}

func formatSlot(slot SlotState) string {
	return fmt.Sprintf("(status:%s version:%d tries:%d)", slot.Status, slot.Version, slot.TriesRemaining)
}

func sameOutput(actual any, expected any) bool {
	actualError, actualIsError := actual.(error)
	expectedError, expectedIsError := expected.(error)
	if actualIsError || expectedIsError {
		return actualIsError == expectedIsError && errors.Is(actualError, expectedError)
	}
	return actual == expected
}

func assertInvariants(t *testing.T, index int, snapshot Snapshot) {
	t.Helper()
	if snapshot.Floor != snapshot.Slots[snapshot.ActiveSlot].Version {
		t.Fatalf("operation %d: floor %d != active version %d", index, snapshot.Floor, snapshot.Slots[snapshot.ActiveSlot].Version)
	}
	if snapshot.Slots[snapshot.ActiveSlot].Status != SlotGood {
		t.Fatalf("operation %d: active slot %d is not Good", index, snapshot.ActiveSlot)
	}
	for slotIndex, slot := range snapshot.Slots {
		if slot.Status == SlotTrial && slot.Version <= snapshot.Floor {
			t.Fatalf("operation %d: Trial slot %d version %d <= floor %d", index, slotIndex, slot.Version, snapshot.Floor)
		}
	}
}

func TestRollbackOccursOnBootMPlusOne(t *testing.T) {
	for _, maxTrialBoots := range []int{1, 3} {
		t.Run(fmt.Sprintf("M%d", maxTrialBoots), func(t *testing.T) {
			manager, err := NewUpgradeManager(4, maxTrialBoots)
			if err != nil {
				t.Fatalf("NewUpgradeManager() error = %v", err)
			}
			if err := manager.Install(5); err != nil {
				t.Fatalf("Install(floor+1) error = %v", err)
			}

			for bootNumber := 1; bootNumber <= maxTrialBoots; bootNumber++ {
				result := manager.Boot()
				t.Logf("M=%d input=Boot #%d; output=%s; decision=trial boot consumes one attempt", maxTrialBoots, bootNumber, formatBoot(result))
				if result != (BootResult{Slot: 1, Version: 5, Trial: true}) {
					t.Fatalf("trial boot %d = %s", bootNumber, formatBoot(result))
				}
			}

			result := manager.Boot()
			after := manager.Snapshot()
			t.Logf("M=%d input=Boot #%d; output=%s; after=%s; decision=no tries remain, so this boot rolls back",
				maxTrialBoots, maxTrialBoots+1, formatBoot(result), formatSnapshot(after))
			if result != (BootResult{Slot: 0, Version: 4, Rollback: true}) {
				t.Fatalf("rollback boot = %s", formatBoot(result))
			}
			if after.Slots[1].Status != SlotBad || after.Floor != 4 {
				t.Fatalf("rollback state = %s", formatSnapshot(after))
			}
		})
	}
}

func TestRejectionsAreAtomicAndDistinguishable(t *testing.T) {
	for _, args := range [][2]int{{0, 1}, {1, 0}, {-1, 2}, {2, -1}} {
		manager, err := NewUpgradeManager(args[0], args[1])
		t.Logf("input=NewUpgradeManager(v0=%d,M=%d); output=(%v,%v); decision=both constructor arguments must be at least 1", args[0], args[1], manager, err)
		if manager != nil || !errors.Is(err, ErrInvalidConstructorArgs) {
			t.Fatalf("NewUpgradeManager(%d, %d) = (%v, %v), want nil and ErrInvalidConstructorArgs", args[0], args[1], manager, err)
		}
	}

	manager, err := NewUpgradeManager(2, 2)
	if err != nil {
		t.Fatalf("NewUpgradeManager() error = %v", err)
	}

	cases := []struct {
		name     string
		run      func() error
		want     error
		decision string
	}{
		{"install zero", func() error { return manager.Install(0) }, ErrInvalidVersion, "zero is checked before floor and trial slot"},
		{"install equal floor", func() error { return manager.Install(2) }, ErrVersionNotRaised, "equal to floor is rejected"},
		{"confirm normal boot", manager.Confirm, ErrNoTrialBoot, "no prior trial boot can be confirmed"},
	}

	for _, tc := range cases {
		before := manager.Snapshot()
		err := tc.run()
		after := manager.Snapshot()
		t.Logf("input=%s; output=%v; decision=%s; before=%s; after=%s", tc.name, err, tc.decision, formatSnapshot(before), formatSnapshot(after))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s error = %v, want %v", tc.name, err, tc.want)
		}
		if after != before {
			t.Fatalf("%s changed state: before=%s after=%s", tc.name, formatSnapshot(before), formatSnapshot(after))
		}
	}

	if err := manager.Install(3); err != nil {
		t.Fatalf("Install(floor+1) error = %v", err)
	}
	before := manager.Snapshot()
	err = manager.Install(4)
	after := manager.Snapshot()
	t.Logf("input=Install during Trial; output=%v; decision=trial inactive slot rejection comes after version gate; before=%s; after=%s", err, formatSnapshot(before), formatSnapshot(after))
	if !errors.Is(err, ErrInactiveSlotOnTrial) || after != before {
		t.Fatalf("Trial Install rejection was not atomic: err=%v after=%s", err, formatSnapshot(after))
	}
}

func TestConcurrentOperationsAreLinearizable(t *testing.T) {
	manager, err := NewUpgradeManager(1, 2)
	if err != nil {
		t.Fatalf("NewUpgradeManager() error = %v", err)
	}

	const goroutines = 4
	const iterations = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for worker := 0; worker < goroutines; worker++ {
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				switch (worker + iteration) % 7 {
				case 0:
					t.Logf("concurrent input=Install(v=%d); output=%v", 2+iteration, manager.Install(2+iteration))
				case 1, 2, 3:
					result := manager.Boot()
					snapshot := manager.Snapshot()
					t.Logf("concurrent input=Boot; output=%s; query=%s", formatBoot(result), formatSnapshot(snapshot))
				case 4:
					t.Logf("concurrent input=Confirm; output=%v", manager.Confirm())
				default:
					snapshot := manager.Snapshot()
					t.Logf("concurrent input=Snapshot; output=%s", formatSnapshot(snapshot))
				}
			}
		}(worker)
	}

	wg.Wait()
	snapshot := manager.Snapshot()
	t.Logf("concurrent final query=%s; decision=single mutex permits only one state transition at a time", formatSnapshot(snapshot))
	assertInvariants(t, -1, snapshot)
}

func formatBoot(result BootResult) string {
	return fmt.Sprintf("{slot:%d version:%d trial:%t rollback:%t}", result.Slot, result.Version, result.Trial, result.Rollback)
}
