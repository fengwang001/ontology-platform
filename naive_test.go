package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type naiveBuffer struct {
	width  int64
	grace  int64
	cap    int
	policy FullPolicy
	st     int64
	late   int64
	items  map[naiveKey]*naiveItem
}

type naiveKey struct {
	key string
	ws  int64
}

type naiveItem struct {
	key    string
	ws     int64
	end    int64
	value  int64
	lastTS int64
}

type naiveResponse struct {
	status  string
	err     error
	emitted []Emitted
}

func newNaive(width, grace int64, cap int, policy FullPolicy) *naiveBuffer {
	return &naiveBuffer{
		width:  width,
		grace:  grace,
		cap:    cap,
		policy: policy,
		st:     -1,
		items:  make(map[naiveKey]*naiveItem),
	}
}

func (n *naiveBuffer) sortedItems() []*naiveItem {
	items := make([]*naiveItem, 0, len(n.items))
	for _, item := range n.items {
		items = append(items, item)
	}
	sortNaive(items)
	return items
}

func sortNaive(items []*naiveItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].end != items[j].end {
			return items[i].end < items[j].end
		}
		return items[i].key < items[j].key
	})
}

func naiveEmit(item *naiveItem, kind EmitKind) Emitted {
	return Emitted{
		Key:    []byte(item.key),
		WS:     item.ws,
		End:    item.end,
		Value:  item.value,
		LastTS: item.lastTS,
		Kind:   kind,
	}
}

func (n *naiveBuffer) update(key string, ts, value int64) naiveResponse {
	if key == "" || ts < 0 || ts > 1_000_000_000_000_000 ||
		value < -1_000_000_000_000 || value > 1_000_000_000_000 {
		return naiveResponse{err: ErrInvalidArgument}
	}

	ws := ts / n.width * n.width
	end := ws + n.width
	if end+n.grace <= n.st {
		n.late++
		return naiveResponse{status: StatusDiscarded}
	}

	nextST := max(n.st, ts)
	closed := make([]*naiveItem, 0)
	open := make([]*naiveItem, 0)
	currentOpen := false
	for _, item := range n.sortedItems() {
		if item.end+n.grace <= nextST {
			closed = append(closed, item)
		} else {
			open = append(open, item)
			if item.key == key && item.ws == ws {
				currentOpen = true
			}
		}
	}

	id := naiveKey{key: key, ws: ws}
	needNew := 1
	if currentOpen {
		needNew = 0
	}
	earlyCount := len(open) + needNew - n.cap
	if earlyCount > 0 && n.policy == Shutdown {
		return naiveResponse{err: ErrBufferFull}
	}
	if earlyCount < 0 {
		earlyCount = 0
	}

	emitted := make([]Emitted, 0, len(closed)+earlyCount)
	for _, item := range closed {
		emitted = append(emitted, naiveEmit(item, Final))
		delete(n.items, naiveKey{key: item.key, ws: item.ws})
	}

	early := make([]*naiveItem, 0, earlyCount)
	remaining := make([]*naiveItem, 0, len(open)-earlyCount)
	for _, item := range open {
		if earlyCount > 0 && !(item.key == key && item.ws == ws) {
			early = append(early, item)
			earlyCount--
		} else {
			remaining = append(remaining, item)
		}
	}
	for _, item := range early {
		emitted = append(emitted, naiveEmit(item, Early))
		delete(n.items, naiveKey{key: item.key, ws: item.ws})
	}
	open = remaining

	if item, ok := n.items[id]; ok {
		item.value = value
		item.lastTS = ts
	} else {
		n.items[id] = &naiveItem{
			key:    key,
			ws:     ws,
			end:    end,
			value:  value,
			lastTS: ts,
		}
	}

	n.st = nextST
	return naiveResponse{status: StatusAccepted, emitted: emitted}
}

func (n *naiveBuffer) tick(t int64) ([]Emitted, error) {
	if t < 0 || t > 1_000_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if t < n.st {
		return nil, ErrStreamTimeRollback
	}
	if t == n.st {
		return []Emitted{}, nil
	}

	emitted := make([]Emitted, 0)
	for _, item := range n.sortedItems() {
		if item.end+n.grace <= t {
			emitted = append(emitted, naiveEmit(item, Final))
			delete(n.items, naiveKey{key: item.key, ws: item.ws})
		}
	}
	n.st = t
	return emitted, nil
}

func TestRandomizedNaiveComparison(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed+10_000)))
		width := int64(1 + rng.IntN(20))
		grace := int64(rng.IntN(8))
		cap := 1 + rng.IntN(6)
		policy := EmitEarly
		if rng.IntN(2) == 0 {
			policy = Shutdown
		}
		actual, err := NewBuffer(width, grace, cap, policy)
		if err != nil {
			t.Fatal(err)
		}
		expected := newNaive(width, grace, cap, policy)
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d S=%d G=%d E=%d policy=%v\n", seed, width, grace, cap, policy)

		for step := range 40 {
			var input string
			var reason string
			var gotResponse naiveResponse
			var wantResponse naiveResponse

			if rng.IntN(4) == 0 {
				tick := int64(-1 + rng.IntN(70))
				input = fmt.Sprintf("tick(%d)", tick)
				reason = tickReason(tick, expected.st)
				gotOut, gotErr := actual.Tick(tick)
				wantOut, wantErr := expected.tick(tick)
				gotResponse = naiveResponse{err: gotErr, emitted: gotOut}
				wantResponse = naiveResponse{err: wantErr, emitted: wantOut}
			} else {
				keyIndex := rng.IntN(6)
				key := []byte{byte('a' + keyIndex)}
				if rng.IntN(25) == 0 {
					key = nil
				}
				ts := int64(-1 + rng.IntN(70))
				value := int64(rng.IntN(21) - 10)
				input = fmt.Sprintf("update(%q,%d,%d)", string(key), ts, value)
				reason = updateReason(string(key), ts, value, expected)
				got, gotErr := actual.Update(key, ts, value)
				want := expected.update(string(key), ts, value)
				gotResponse = naiveResponse{status: got.Status, err: gotErr, emitted: got.Emitted}
				wantResponse = want
			}

			fmt.Fprintf(&log, "step=%d %s => %s; reason=%s\n", step, input, formatNaiveResponse(gotResponse), reason)
			if !sameNaiveResponse(gotResponse, wantResponse) {
				t.Fatalf("seed=%d step=%d %s\ngot %s\nwant %s\n\n%s", seed, step, input, formatNaiveResponse(gotResponse), formatNaiveResponse(wantResponse), log.String())
			}
			assertSameSnapshot(t, actual, expected, log.String())
		}
		t.Log(log.String())
	}
}

func tickReason(t, streamTime int64) string {
	switch {
	case t < 0 || t > 1_000_000_000_000_000:
		return "invalid tick"
	case t < streamTime:
		return "stream time rollback"
	case t == streamTime:
		return "equal stream time no-op"
	default:
		return "close final entries"
	}
}

func updateReason(key string, ts, value int64, n *naiveBuffer) string {
	if key == "" || ts < 0 || ts > 1_000_000_000_000_000 ||
		value < -1_000_000_000_000 || value > 1_000_000_000_000 {
		return "invalid update"
	}
	ws := ts / n.width * n.width
	if ws+n.width+n.grace <= n.st {
		return "late discard"
	}
	nextST := max(n.st, ts)
	id := naiveKey{key: key, ws: ws}
	open := 0
	for _, item := range n.items {
		if item.end+n.grace > nextST && !(item.key == key && item.ws == ws) {
			open++
		}
	}
	currentOpen := false
	if item, exists := n.items[id]; exists && item.end+n.grace > nextST {
		currentOpen = true
	}
	needNew := 1
	if currentOpen {
		needNew = 0
	}
	if open+needNew > n.cap && n.policy == Shutdown {
		return "buffer full shutdown"
	}
	if open+needNew > n.cap {
		return fmt.Sprintf("emit early %d", open+needNew-n.cap)
	}
	return "accept"
}

func formatNaiveResponse(response naiveResponse) string {
	if response.err != nil {
		return "error=" + response.err.Error()
	}
	if response.status == StatusDiscarded {
		return "Discarded"
	}
	return fmt.Sprintf("Accepted emitted=%v", response.emitted)
}

func sameNaiveResponse(got, want naiveResponse) bool {
	if !errors.Is(got.err, want.err) {
		return false
	}
	if got.status != want.status || len(got.emitted) != len(want.emitted) {
		return false
	}
	return emittedEqual(got.emitted, want.emitted)
}

func assertSameSnapshot(t *testing.T, actual *Buffer, expected *naiveBuffer, log string) {
	t.Helper()
	if actual.StreamTime() != expected.st || actual.Late() != expected.late {
		t.Fatalf("snapshot ST got=%d want=%d late got=%d want=%d\n%s", actual.StreamTime(), expected.st, actual.Late(), expected.late, log)
	}
	gotEntries := actual.Buffered()
	wantItems := expected.sortedItems()
	if len(gotEntries) != len(wantItems) {
		t.Fatalf("buffer size got=%d want=%d\n%s", len(gotEntries), len(wantItems), log)
	}
	for i := range wantItems {
		if string(gotEntries[i].Key) != wantItems[i].key || gotEntries[i].WS != wantItems[i].ws ||
			gotEntries[i].End != wantItems[i].end || gotEntries[i].Value != wantItems[i].value ||
			gotEntries[i].LastTS != wantItems[i].lastTS {
			t.Fatalf("buffer[%d] got=%+v want=%+v\n%s", i, gotEntries[i], wantItems[i], log)
		}
	}
}
