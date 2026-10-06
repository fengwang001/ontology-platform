package thinpool

import (
	"errors"
	"math/rand"
	"testing"
)

type naiveVolume struct {
	virtual uint64
	reserve uint64
	blocks  map[uint64]uint64
}

type naiveModel struct {
	physical     uint64
	overcommit   uint64
	warning      uint64
	critical     uint64
	volumes      map[string]naiveVolume
	free         map[uint64]struct{}
	nextPhysical uint64
	totalVirtual uint64
	events       []Event
	nextEvent    uint64
	level        WaterLevel
}

func newNaiveModel(physical, overcommit, warning, critical uint64) *naiveModel {
	model := &naiveModel{
		physical:   physical,
		overcommit: overcommit,
		warning:    warning,
		critical:   critical,
		volumes:    make(map[string]naiveVolume),
		free:       make(map[uint64]struct{}),
		level:      LevelNormal,
	}
	model.level = model.levelFor(0)
	return model
}

func (m *naiveModel) allocated() uint64 {
	return m.physical - uint64(len(m.free)) - (m.physical - m.nextPhysical)
}

func (m *naiveModel) freeCount() uint64 {
	return m.physical - m.allocated()
}

func (m *naiveModel) totalReserve() uint64 {
	var total uint64
	for _, vol := range m.volumes {
		total += vol.reserve
	}
	return total
}

func (m *naiveModel) deficitOf(vol naiveVolume) uint64 {
	if vol.reserve > uint64(len(vol.blocks)) {
		return vol.reserve - uint64(len(vol.blocks))
	}
	return 0
}

func (m *naiveModel) totalDeficit() uint64 {
	var total uint64
	for _, vol := range m.volumes {
		total += m.deficitOf(vol)
	}
	return total
}

func (m *naiveModel) allocatePhysical() (uint64, bool) {
	for block := uint64(0); block < m.physical; block++ {
		if _, freed := m.free[block]; freed {
			delete(m.free, block)
			return block, true
		}
	}
	if m.nextPhysical < m.physical {
		block := m.nextPhysical
		m.nextPhysical++
		return block, true
	}
	return 0, false
}

func (m *naiveModel) create(name string, virtual, reserve uint64) ErrorCode {
	if name == "" {
		return ErrInvalidArgument
	}
	if _, exists := m.volumes[name]; exists {
		return ErrExists
	}
	if m.totalVirtual+virtual > (m.physical*m.overcommit)/100 {
		return ErrOvercommit
	}
	if m.totalReserve()+reserve > m.physical {
		return ErrReservePool
	}
	if reserve > virtual {
		return ErrReserveVolume
	}
	if reserve > m.freeCount()-m.totalDeficit() {
		return ErrInsufficient
	}

	m.volumes[name] = naiveVolume{
		virtual: virtual,
		reserve: reserve,
		blocks:  make(map[uint64]uint64),
	}
	m.totalVirtual += virtual
	return ""
}

func (m *naiveModel) write(name string, block uint64) ErrorCode {
	if name == "" {
		return ErrInvalidArgument
	}
	vol, exists := m.volumes[name]
	if !exists {
		return ErrNotFound
	}
	if block >= vol.virtual {
		return ErrInvalidArgument
	}
	if _, mapped := vol.blocks[block]; mapped {
		return ""
	}
	remainingDeficit := m.totalDeficit() - m.deficitOf(vol)
	if m.freeCount() <= remainingDeficit {
		return ErrPoolExhausted
	}
	physical, ok := m.allocatePhysical()
	if !ok {
		return ErrPoolExhausted
	}
	vol.blocks[block] = physical
	m.volumes[name] = vol
	m.maybeEvent()
	return ""
}

func (m *naiveModel) reclaim(name string, start, length uint64) (uint64, ErrorCode) {
	if name == "" {
		return 0, ErrInvalidArgument
	}
	vol, exists := m.volumes[name]
	if !exists {
		return 0, ErrNotFound
	}
	if length == 0 {
		return 0, ""
	}
	if start+length < start || start+length > vol.virtual {
		return 0, ErrInvalidArgument
	}

	var released uint64
	for block := start; block < start+length; block++ {
		physical, mapped := vol.blocks[block]
		if !mapped {
			continue
		}
		delete(vol.blocks, block)
		m.free[physical] = struct{}{}
		released++
	}
	m.volumes[name] = vol
	if released > 0 {
		m.maybeEvent()
	}
	return released, ""
}

func (m *naiveModel) resize(name string, newVirtual uint64) ErrorCode {
	if name == "" {
		return ErrInvalidArgument
	}
	vol, exists := m.volumes[name]
	if !exists {
		return ErrNotFound
	}

	if newVirtual > vol.virtual {
		if m.totalVirtual+(newVirtual-vol.virtual) > (m.physical*m.overcommit)/100 {
			return ErrOvercommit
		}
	}
	if vol.reserve > newVirtual {
		return ErrReserveVolume
	}
	if newVirtual < vol.virtual {
		for block := newVirtual; block < vol.virtual; block++ {
			if _, mapped := vol.blocks[block]; mapped {
				return ErrVolumeHasData
			}
		}
	}

	m.totalVirtual = m.totalVirtual - vol.virtual + newVirtual
	vol.virtual = newVirtual
	m.volumes[name] = vol
	return ""
}

func (m *naiveModel) setReserve(name string, reserve uint64) ErrorCode {
	if name == "" {
		return ErrInvalidArgument
	}
	vol, exists := m.volumes[name]
	if !exists {
		return ErrNotFound
	}
	if m.totalReserve()-vol.reserve+reserve > m.physical {
		return ErrReservePool
	}
	if reserve > vol.virtual {
		return ErrReserveVolume
	}

	newDeficit := uint64(0)
	if reserve > uint64(len(vol.blocks)) {
		newDeficit = reserve - uint64(len(vol.blocks))
	}
	if m.totalDeficit()-m.deficitOf(vol)+newDeficit > m.freeCount() {
		return ErrInsufficient
	}

	vol.reserve = reserve
	m.volumes[name] = vol
	return ""
}

func (m *naiveModel) delete(name string) ErrorCode {
	if name == "" {
		return ErrInvalidArgument
	}
	vol, exists := m.volumes[name]
	if !exists {
		return ErrNotFound
	}

	for _, physical := range vol.blocks {
		m.free[physical] = struct{}{}
	}
	m.totalVirtual -= vol.virtual
	delete(m.volumes, name)
	m.maybeEvent()
	return ""
}

func (m *naiveModel) maybeEvent() {
	allocated := m.allocated()
	newLevel := m.levelFor(allocated)
	if newLevel == m.level {
		return
	}
	m.nextEvent++
	m.events = append(m.events, Event{
		Sequence:  m.nextEvent,
		OldLevel:  m.level,
		NewLevel:  newLevel,
		Allocated: allocated,
	})
	m.level = newLevel
}

func (m *naiveModel) levelFor(allocated uint64) WaterLevel {
	usage := allocated * 100
	if usage >= m.physical*m.critical {
		return LevelCritical
	}
	if usage >= m.physical*m.warning {
		return LevelWarning
	}
	return LevelNormal
}

func errorCodeFromError(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var target Error
	if errors.As(err, &target) {
		return target.Code
	}
	return "unknown"
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	const physical = uint64(12)
	pool, err := NewPool(physical, 180, 40, 70)
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel(physical, 180, 40, 70)
	random := rand.New(rand.NewSource(1599))
	names := []string{"a", "b", "c", "d"}

	for iteration := 0; iteration < 1200; iteration++ {
		name := names[random.Intn(len(names))]
		operation := random.Intn(8)
		switch operation {
		case 0, 1:
			virtual := uint64(random.Intn(10))
			reserve := uint64(random.Intn(6))
			t.Logf("op=%d create name=%s virtual=%d reserve=%d", iteration, name, virtual, reserve)
			err := pool.CreateVolume(name, virtual, reserve)
			code := model.create(name, virtual, reserve)
			t.Logf("result=%v model=%s", err, code)
			if errorCodeFromError(err) != code {
				t.Fatalf("create mismatch")
			}
		case 2:
			block := uint64(random.Intn(10))
			t.Logf("op=%d write name=%s block=%d", iteration, name, block)
			err := pool.WriteBlock(name, block)
			code := model.write(name, block)
			t.Logf("result=%v model=%s", err, code)
			if errorCodeFromError(err) != code {
				t.Fatalf("write mismatch")
			}
		case 3:
			start := uint64(random.Intn(10))
			length := uint64(random.Intn(5))
			t.Logf("op=%d reclaim name=%s start=%d length=%d", iteration, name, start, length)
			released, err := pool.ReclaimRange(name, start, length)
			modelReleased, code := model.reclaim(name, start, length)
			t.Logf("result=(%d,%v) model=(%d,%s)", released, err, modelReleased, code)
			if released != modelReleased || errorCodeFromError(err) != code {
				t.Fatalf("reclaim mismatch")
			}
		case 4:
			newVirtual := uint64(random.Intn(10))
			t.Logf("op=%d resize name=%s newVirtual=%d", iteration, name, newVirtual)
			err := pool.ResizeVolume(name, newVirtual)
			code := model.resize(name, newVirtual)
			t.Logf("result=%v model=%s", err, code)
			if errorCodeFromError(err) != code {
				t.Fatalf("resize mismatch")
			}
		case 5:
			reserve := uint64(random.Intn(6))
			t.Logf("op=%d setReserve name=%s reserve=%d", iteration, name, reserve)
			err := pool.SetReservation(name, reserve)
			code := model.setReserve(name, reserve)
			t.Logf("result=%v model=%s", err, code)
			if errorCodeFromError(err) != code {
				t.Fatalf("reserve mismatch")
			}
		case 6:
			t.Logf("op=%d delete name=%s", iteration, name)
			err := pool.DeleteVolume(name)
			code := model.delete(name)
			t.Logf("result=%v model=%s", err, code)
			if errorCodeFromError(err) != code {
				t.Fatalf("delete mismatch")
			}
		case 7:
			actual := pool.Snapshot()
			t.Logf("op=%d snapshot allocated=%d free=%d deficit=%d", iteration, actual.Allocated, actual.Free, actual.TotalDeficit)
			if actual.Allocated != model.allocated() || actual.Free != model.freeCount() || actual.TotalVirtual != model.totalVirtual || actual.TotalReserved != model.totalReserve() || actual.TotalDeficit != model.totalDeficit() {
				t.Fatalf("counter mismatch: actual=%+v model allocated=%d free=%d virtual=%d reserve=%d deficit=%d", actual, model.allocated(), model.freeCount(), model.totalVirtual, model.totalReserve(), model.totalDeficit())
			}
			if len(actual.Volumes) != len(model.volumes) {
				t.Fatalf("volume count mismatch")
			}
			for volumeName, actualVolume := range actual.Volumes {
				modelVolume := model.volumes[volumeName]
				if actualVolume.Virtual != modelVolume.virtual || actualVolume.Reserved != modelVolume.reserve || actualVolume.Used != uint64(len(modelVolume.blocks)) {
					t.Fatalf("volume mismatch for %s", volumeName)
				}
				if len(actualVolume.Physical) != len(modelVolume.blocks) {
					t.Fatalf("mapping length mismatch for %s", volumeName)
				}
				for virtualBlock, physicalBlock := range actualVolume.Physical {
					if modelVolume.blocks[virtualBlock] != physicalBlock {
						t.Fatalf("physical mapping mismatch for %s[%d]", volumeName, virtualBlock)
					}
				}
			}
			actualEvents := pool.Events()
			if len(actualEvents) != len(model.events) {
				t.Fatalf("event length mismatch")
			}
			for index := range actualEvents {
				if actualEvents[index] != model.events[index] {
					t.Fatalf("event mismatch at %d: actual=%+v model=%+v", index, actualEvents[index], model.events[index])
				}
			}
		}
		if operation != 7 {
			t.Logf("basis allocated=%d free=%d virtual=%d reserve=%d deficit=%d level=%s",
				model.allocated(), model.freeCount(), model.totalVirtual, model.totalReserve(),
				model.totalDeficit(), model.levelFor(model.allocated()))
		}
	}
}
