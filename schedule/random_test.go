package schedule

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type modelSurgery struct {
	id        string
	room      string
	start     int64
	end       int64
	surgeon   string
	needs     map[string]int
	emergency bool
}

type modelState struct {
	now     int64
	started bool
	rooms   map[string]int64
	equips  map[string]equipInfo
	list    []modelSurgery
}

type equipInfo struct {
	count int
	st    int64
}

func TestRandomSchedulesAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			actual := NewScheduler()
			expected := &modelState{
				rooms:  map[string]int64{},
				equips: map[string]equipInfo{},
			}
			runRandomCase(t, rng, actual, expected, seed)
		})
	}
}

func runRandomCase(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, seed int64) {
	t.Helper()
	var log strings.Builder
	logf := func(format string, args ...any) {
		log.WriteString(fmt.Sprintf(format+"\n", args...))
	}
	logf("seed=%d random schedule begins", seed)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(log.String())
		}
	})
	for i := 0; i < 45; i++ {
		switch {
		case len(expected.rooms) == 0 && rng.Intn(3) == 0:
			runRandomRoom(t, rng, actual, expected, logf)
		case len(expected.equips) == 0 && rng.Intn(3) == 0:
			runRandomEquip(t, rng, actual, expected, logf)
		case rng.Intn(10) < 6:
			runRandomBook(t, rng, actual, expected, logf)
		case rng.Intn(10) < 8:
			runRandomEmergency(t, rng, actual, expected, logf)
		default:
			runRandomCancel(t, rng, actual, expected, logf)
		}
		verifyActual(t, actual, "actual invariant")
	}
}

type caseLogger func(format string, args ...any)

func runRandomRoom(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, logf caseLogger) {
	id := randomRoomID(rng)
	turn := int64(rng.Intn(5))
	got := actual.AddRoom(id, turn)
	_, exists := expected.rooms[id]
	want := error(nil)
	if id == "" || turn < 0 || turn > 240 {
		want = ErrInvalidArgument
	} else if exists {
		want = ErrExists
	}
	if !errors.Is(got, want) {
		t.Fatalf("AddRoom(%q,%d) = %v, want %v", id, turn, got, want)
	}
	logf("input AddRoom(%q,%d) output=%v basis=%v", id, turn, got, want)
	if got == nil {
		expected.rooms[id] = turn
	}
}

func runRandomEquip(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, logf caseLogger) {
	id := randomEquipID(rng)
	count := 1 + rng.Intn(2)
	st := int64(rng.Intn(4))
	got := actual.AddEquip(id, count, st)
	_, exists := expected.equips[id]
	want := error(nil)
	if id == "" || count < 1 || count > 100 || st < 0 || st > 240 {
		want = ErrInvalidArgument
	} else if exists {
		want = ErrExists
	}
	if !errors.Is(got, want) {
		t.Fatalf("AddEquip(%q,%d,%d) = %v, want %v", id, count, st, got, want)
	}
	logf("input AddEquip(%q,%d,%d) output=%v basis=%v", id, count, st, got, want)
	if got == nil {
		expected.equips[id] = equipInfo{count: count, st: st}
	}
}

func runRandomBook(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, logf caseLogger) {
	if len(expected.rooms) == 0 {
		return
	}
	now := expected.now
	if rng.Intn(5) != 0 {
		now += int64(rng.Intn(4))
	}
	id := randomID(rng)
	if len(expected.list) > 0 && rng.Intn(5) == 0 {
		id = expected.list[rng.Intn(len(expected.list))].id
	}
	roomID := mapKeys(expected.rooms)[rng.Intn(len(expected.rooms))]
	start := now + int64(rng.Intn(12))
	dur := int64(1 + rng.Intn(8))
	surgeon := "doc" + string(rune('a'+rng.Intn(4)))
	needs := randomNeeds(rng, expected)
	got := actual.Book(now, id, roomID, start, dur, surgeon, Needs(needs))
	want := modelBookError(expected, now, id, roomID, start, dur, surgeon, needs)
	if !sameTypedError(got, want) {
		t.Fatalf("Book(now=%d id=%s room=%s start=%d dur=%d doc=%s needs=%v) = %v, want %v",
			now, id, roomID, start, dur, surgeon, needs, got, want)
	}
	logf("input Book now=%d id=%s room=%s [%d,%d) doc=%s needs=%v output=%v basis=%v",
		now, id, roomID, start, start+dur, surgeon, needs, got, want)
	if got == nil {
		expected.now = now
		expected.started = true
		expected.list = append(expected.list, modelSurgery{
			id: id, room: roomID, start: start, end: start + dur, surgeon: surgeon, needs: cloneModelNeeds(needs),
		})
	}
}

func runRandomEmergency(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, logf caseLogger) {
	if len(expected.rooms) == 0 {
		return
	}
	now := expected.now
	if rng.Intn(5) != 0 {
		now += int64(rng.Intn(4))
	}
	id := randomID(rng)
	if len(expected.list) > 0 && rng.Intn(6) == 0 {
		id = expected.list[rng.Intn(len(expected.list))].id
	}
	dur := int64(1 + rng.Intn(8))
	surgeon := "doc" + string(rune('a'+rng.Intn(4)))
	needs := randomNeeds(rng, expected)
	gotResult, got := actual.Emergency(now, id, dur, surgeon, Needs(needs))
	wantRoom, wantStart, wantDisplaced, want := modelEmergency(expected, now, id, dur, surgeon, needs)
	if !sameTypedError(got, want) {
		t.Fatalf("Emergency(now=%d id=%s dur=%d doc=%s needs=%v) = %+v,%v want error %v",
			now, id, dur, surgeon, needs, gotResult, got, want)
	}
	if got == nil && (gotResult.Room != wantRoom || gotResult.Start != wantStart || !sameDisplaced(gotResult.Displaced, wantDisplaced)) {
		t.Fatalf("Emergency result %+v want room=%s start=%d displaced=%v", gotResult, wantRoom, wantStart, wantDisplaced)
	}
	logf("input Emergency now=%d id=%s dur=%d doc=%s needs=%v output={room:%s start:%d displaced:%v} err=%v basis=room/doctor/equipment",
		now, id, dur, surgeon, needs, gotResult.Room, gotResult.Start, displacedIDs(gotResult.Displaced), got)
}

func runRandomCancel(t *testing.T, rng *rand.Rand, actual *Scheduler, expected *modelState, logf caseLogger) {
	now := expected.now
	if rng.Intn(3) == 0 {
		now += int64(rng.Intn(6))
	}
	id := "gone"
	if len(expected.list) > 0 {
		id = expected.list[rng.Intn(len(expected.list))].id
	}
	got := actual.Cancel(now, id)
	want := modelCancelError(expected, now, id)
	if !errors.Is(got, want) {
		t.Fatalf("Cancel(now=%d,id=%s)=%v want %v", now, id, got, want)
	}
	logf("input Cancel now=%d id=%s output=%v basis=%v", now, id, got, want)
	if got == nil {
		expected.now = now
		expected.started = true
		removeModel(expected, id)
	}
}

func verifyActual(t *testing.T, s *Scheduler, reason string) {
	t.Helper()
	for i := 0; i < len(s.list); i++ {
		for j := i + 1; j < len(s.list); j++ {
			left, right := s.list[i], s.list[j]
			if left.Room == right.Room {
				info, _ := s.rooms.Get(left.Room)
				if roomConflicts(left.Start, left.End, right.Start, right.End, info.Turn) {
					t.Fatalf("%s: room conflict %s %s", reason, left.ID, right.ID)
				}
			}
			if left.Surgeon == right.Surgeon && overlap(left.Start, left.End, right.Start, right.End) {
				t.Fatalf("%s: surgeon conflict %s %s", reason, left.ID, right.ID)
			}
		}
	}
	for _, typ := range s.equips.Names() {
		info, _ := s.equips.Get(typ)
		load := map[int64]int{}
		for _, surgery := range s.list {
			if surgery.Needs[typ] == 0 {
				continue
			}
			for minute := surgery.Start; minute < surgery.End+info.Sterilize; minute++ {
				load[minute] += surgery.Needs[typ]
				if load[minute] > info.Count {
					t.Fatalf("%s: equipment %s overloaded at minute %d", reason, typ, minute)
				}
			}
		}
	}
}

func roomConflicts(aStart, aEnd, bStart, bEnd, turn int64) bool {
	return aStart < bEnd+turn && bStart < aEnd+turn
}

func overlap(aStart, aEnd, bStart, bEnd int64) bool {
	return aStart < bEnd && bStart < aEnd
}

func sameErrorClass(got, want error) bool {
	return errors.Is(got, want)
}

func sortedModelIDs(list []modelSurgery) []string {
	ids := make([]string, 0, len(list))
	for _, surgery := range list {
		ids = append(ids, surgery.id)
	}
	sort.Strings(ids)
	return ids
}

func randomRoomID(rng *rand.Rand) string {
	if rng.Intn(8) == 0 {
		return ""
	}
	return string(rune('1' + rng.Intn(4)))
}

func randomEquipID(rng *rand.Rand) string {
	if rng.Intn(8) == 0 {
		return ""
	}
	return string(rune('a' + rng.Intn(3)))
}

func randomID(rng *rand.Rand) string {
	return "s" + string(rune('a'+rng.Intn(18)))
}

func randomNeeds(rng *rand.Rand, state *modelState) map[string]int {
	if len(state.equips) == 0 || rng.Intn(2) == 0 {
		return nil
	}
	needs := map[string]int{}
	for id, info := range state.equips {
		if rng.Intn(2) == 0 {
			needs[id] = 1 + rng.Intn(info.count)
		}
	}
	return needs
}

func mapKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func modelCommon(state *modelState, now int64, id, roomID string, start, dur int64, surgeon string, needs map[string]int) (modelSurgery, error) {
	if now < 0 || start < 0 || start > 1_000_000_000 || dur < 1 || dur > 1440 || start+dur > 1_000_000_000 ||
		id == "" || roomID == "" || surgeon == "" || len(needs) > 8 {
		return modelSurgery{}, ErrInvalidArgument
	}
	for typ, count := range needs {
		if typ == "" || count < 1 || count > 100 {
			return modelSurgery{}, ErrInvalidArgument
		}
	}
	if state.started && now < state.now {
		return modelSurgery{}, ErrClockRolledBack
	}
	for _, surgery := range state.list {
		if surgery.id == id {
			return modelSurgery{}, ErrIDExists
		}
	}
	if _, ok := state.rooms[roomID]; !ok {
		return modelSurgery{}, ErrUnknownRoom
	}
	for typ := range needs {
		if _, ok := state.equips[typ]; !ok {
			return modelSurgery{}, ErrUnknownEquip
		}
	}
	for typ, count := range needs {
		if state.equips[typ].count < count {
			return modelSurgery{}, ErrInvalidArgument
		}
	}
	return modelSurgery{id: id, room: roomID, start: start, end: start + dur, surgeon: surgeon, needs: needs}, nil
}

func modelBookError(state *modelState, now int64, id, roomID string, start, dur int64, surgeon string, needs map[string]int) error {
	candidate, err := modelCommon(state, now, id, roomID, start, dur, surgeon, needs)
	if err != nil {
		return err
	}
	if start < now {
		return ErrInvalidArgument
	}
	turn := state.rooms[roomID]
	for _, old := range state.list {
		if old.room == roomID && roomConflicts(candidate.start, candidate.end, old.start, old.end, turn) {
			return ErrRoomConflict
		}
	}
	for _, old := range state.list {
		if old.surgeon == surgeon && overlap(candidate.start, candidate.end, old.start, old.end) {
			return ErrSurgeonConflict
		}
	}
	names := make([]string, 0, len(needs))
	for typ := range needs {
		names = append(names, typ)
	}
	sort.Strings(names)
	for _, typ := range names {
		if modelEquipmentShortfall(state, candidate, nil, typ) > 0 {
			return TypeError{Err: ErrEquipConflict, Type: typ}
		}
	}
	return nil
}

func modelCancelError(state *modelState, now int64, id string) error {
	if now < 0 || id == "" {
		return ErrInvalidArgument
	}
	if state.started && now < state.now {
		return ErrClockRolledBack
	}
	for _, surgery := range state.list {
		if surgery.id == id {
			if surgery.emergency || surgery.start <= now {
				return ErrBadState
			}
			return nil
		}
	}
	return ErrBadState
}

func modelEmergency(state *modelState, now int64, id string, dur int64, surgeon string, needs map[string]int) (string, int64, []modelSurgery, error) {
	if now < 0 || dur < 1 || dur > 1440 || now+dur > 1_000_000_000 || id == "" || surgeon == "" || len(needs) > 8 {
		return "", 0, nil, ErrInvalidArgument
	}
	for typ, count := range needs {
		if typ == "" || count < 1 || count > 100 {
			return "", 0, nil, ErrInvalidArgument
		}
	}
	if state.started && now < state.now {
		return "", 0, nil, ErrClockRolledBack
	}
	for _, surgery := range state.list {
		if surgery.id == id {
			return "", 0, nil, ErrIDExists
		}
	}
	for typ := range needs {
		if _, ok := state.equips[typ]; !ok {
			return "", 0, nil, ErrUnknownEquip
		}
	}
	for typ, count := range needs {
		if state.equips[typ].count < count {
			return "", 0, nil, ErrInvalidArgument
		}
	}
	if len(state.rooms) == 0 {
		return "", 0, nil, ErrNoRoom
	}
	candidate := modelSurgery{id: id, start: now, end: now + dur, surgeon: surgeon, needs: needs}
	type picked struct {
		id        string
		start     int64
		displaced []modelSurgery
	}
	var choices []picked
	for _, roomID := range mapKeys(state.rooms) {
		turn := state.rooms[roomID]
		start := now
		for _, old := range state.list {
			if old.room == roomID && !modelDisplaceable(old, now) && old.end+turn > start {
				start = old.end + turn
			}
		}
		var displaced []modelSurgery
		candidateRoom := modelSurgery{room: roomID, start: start, end: start + dur}
		for _, old := range state.list {
			if old.room == roomID && modelDisplaceable(old, now) &&
				roomConflicts(candidateRoom.start, candidateRoom.end, old.start, old.end, turn) {
				displaced = append(displaced, old)
			}
		}
		sort.Slice(displaced, func(i, j int) bool { return modelSurgeryLess(displaced[i], displaced[j]) })
		choices = append(choices, picked{id: roomID, start: start, displaced: displaced})
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].start != choices[j].start {
			return choices[i].start < choices[j].start
		}
		if len(choices[i].displaced) != len(choices[j].displaced) {
			return len(choices[i].displaced) < len(choices[j].displaced)
		}
		return choices[i].id < choices[j].id
	})
	choice := choices[0]
	candidate.room = choice.id
	candidate.start = choice.start
	candidate.end = choice.start + dur
	candidate.emergency = true
	removed := map[string]bool{}
	displaced := append([]modelSurgery{}, choice.displaced...)
	for _, surgery := range displaced {
		removed[surgery.id] = true
	}
	var doctorStep []modelSurgery
	for _, old := range state.list {
		if removed[old.id] || old.surgeon != surgeon || !overlap(candidate.start, candidate.end, old.start, old.end) {
			continue
		}
		if !modelDisplaceable(old, now) {
			return "", 0, nil, ErrSurgeonConflict
		}
		doctorStep = append(doctorStep, old)
	}
	sort.Slice(doctorStep, func(i, j int) bool { return modelSurgeryLess(doctorStep[i], doctorStep[j]) })
	for _, surgery := range doctorStep {
		removed[surgery.id] = true
		displaced = append(displaced, surgery)
	}
	for _, typ := range sortedNeedNames(needs) {
		for modelEquipmentShortfall(state, candidate, removed, typ) > 0 {
			var possible []modelSurgery
			info := state.equips[typ]
			for _, old := range state.list {
				if removed[old.id] || !modelDisplaceable(old, now) || old.needs[typ] == 0 {
					continue
				}
				if overlap(candidate.start, candidate.end+info.st, old.start, old.end+info.st) {
					possible = append(possible, old)
				}
			}
			sort.Slice(possible, func(i, j int) bool {
				if possible[i].start != possible[j].start {
					return possible[i].start > possible[j].start
				}
				return possible[i].id > possible[j].id
			})
			if len(possible) == 0 {
				return "", 0, nil, TypeError{Err: ErrEquipConflict, Type: typ}
			}
			removed[possible[0].id] = true
			displaced = append(displaced, possible[0])
		}
	}
	kept := state.list[:0]
	for _, surgery := range state.list {
		if !removed[surgery.id] {
			kept = append(kept, surgery)
		}
	}
	state.list = append(kept, candidate)
	state.now = now
	state.started = true
	return choice.id, choice.start, displaced, nil
}

func modelEquipmentShortfall(state *modelState, candidate modelSurgery, removed map[string]bool, typ string) int {
	info := state.equips[typ]
	load := map[int64]int{}
	for minute := candidate.start; minute < candidate.end+info.st; minute++ {
		load[minute] += candidate.needs[typ]
	}
	for _, old := range state.list {
		if removed[old.id] || old.needs[typ] == 0 {
			continue
		}
		for minute := old.start; minute < old.end+info.st; minute++ {
			if minute >= candidate.start && minute < candidate.end+info.st {
				load[minute] += old.needs[typ]
			}
		}
	}
	shortfall := 0
	for _, used := range load {
		if used-info.count > shortfall {
			shortfall = used - info.count
		}
	}
	return shortfall
}

func modelDisplaceable(surgery modelSurgery, now int64) bool {
	return !surgery.emergency && surgery.start > now
}

func modelSurgeryLess(a, b modelSurgery) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return a.id < b.id
}

func sortedNeedNames(needs map[string]int) []string {
	names := make([]string, 0, len(needs))
	for typ := range needs {
		names = append(names, typ)
	}
	sort.Strings(names)
	return names
}

func cloneModelNeeds(needs map[string]int) map[string]int {
	cloned := make(map[string]int, len(needs))
	for typ, count := range needs {
		cloned[typ] = count
	}
	return cloned
}

func removeModel(state *modelState, id string) {
	for i, surgery := range state.list {
		if surgery.id == id {
			state.list = append(state.list[:i], state.list[i+1:]...)
			return
		}
	}
}

func sameTypedError(got, want error) bool {
	var gotType, wantType TypeError
	gotHasType := errors.As(got, &gotType)
	wantHasType := errors.As(want, &wantType)
	if gotHasType != wantHasType || (gotHasType && gotType.Type != wantType.Type) {
		return false
	}
	return errors.Is(got, want)
}

func sameDisplaced(got []Surgery, want []modelSurgery) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].ID != want[i].id || got[i].Start != want[i].start {
			return false
		}
	}
	return true
}

func displacedIDs(list []Surgery) []string {
	ids := make([]string, 0, len(list))
	for _, surgery := range list {
		ids = append(ids, surgery.ID)
	}
	return ids
}
