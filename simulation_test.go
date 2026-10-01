package ontology

import (
	"fmt"
	"strings"
	"testing"
)

type simulatedOperation struct {
	kind      string
	clientID  string
	keepAlive int64
	will      *Will
	normal    bool
	now       int64
	wantErr   error
	reason    string
}

type simulatedConnection struct {
	keepAlive int64
	lastSeen  int64
	will      *Will
}

type simulatedPending struct {
	clientID string
	will     Will
	at       int64
	order    int64
}

type naiveRegistry struct {
	now          int64
	hasNow       bool
	connections  map[string]simulatedConnection
	pending      map[string]simulatedPending
	publications []Publication
	nextOrder    int64
}

func newNaiveRegistry() *naiveRegistry {
	return &naiveRegistry{
		connections: make(map[string]simulatedConnection),
		pending:     make(map[string]simulatedPending),
	}
}

func TestNaiveStepSimulation(t *testing.T) {
	operations := []simulatedOperation{
		{kind: "connect", clientID: "b", keepAlive: 1, will: &Will{Topic: "will/b", Payload: []byte("B"), DelayMillis: 200}, now: 0, reason: "connection itself records activity at 0"},
		{kind: "connect", clientID: "a", keepAlive: 1, will: &Will{Topic: "will/a", Payload: []byte("A"), DelayMillis: 0}, now: 0, reason: "keep-alive scan is by ascending client id"},
		{kind: "activity", clientID: "b", now: 1500, reason: "elapsed equals 1500*K, so strict timeout is false"},
		{kind: "advance", now: 1501, wantErr: nil, reason: "a and b time out; a has D=0 and publishes in this same processing"},
		{kind: "activity", clientID: "a", now: 1501, wantErr: ErrClientNotOnline, reason: "not-online rejection happens after entry processing already published a"},
		{kind: "connect", clientID: "b", keepAlive: 1, will: &Will{Topic: "will/b2", Payload: []byte("B2"), DelayMillis: 100}, now: 1600, reason: "b is offline, so this is not takeover and its old pending will remains waiting"},
		{kind: "connect", clientID: "b", keepAlive: 0, will: &Will{Topic: "will/b3", Payload: []byte("B3"), DelayMillis: 100}, now: 1650, reason: "b is online, takeover replaces attached b2 and cancels the old waiting will"},
		{kind: "disconnect", clientID: "b", normal: true, now: 1700, reason: "normal disconnect voids attached b3"},
		{kind: "advance", now: 1701, reason: "old b will was due at 1701 but takeover already canceled it; voided wills do not publish"},
		{kind: "connect", clientID: "c", keepAlive: 0, will: &Will{Topic: "will/c", Payload: []byte("C"), DelayMillis: 100}, now: 2000, reason: "schedule a pending will for exact-time reconnect"},
		{kind: "disconnect", clientID: "c", normal: false, now: 2000, reason: "abnormal disconnect schedules publication at 2100"},
		{kind: "connect", clientID: "c", keepAlive: 0, will: &Will{Topic: "will/c2", Payload: []byte("C2"), DelayMillis: 500}, now: 2100, reason: "entry publishes the due old will before the new connection is installed"},
		{kind: "advance", now: 2200, reason: "advance before rewind to keep later validation meaningful"},
		{kind: "advance", now: 2199, wantErr: ErrClockRewound, reason: "clock rewind is rejected and changes no state"},
	}

	registry := NewRegistry()
	naive := newNaiveRegistry()
	var log strings.Builder

	for index, operation := range operations {
		beforeReal := len(registry.Publications())
		realErr := runOperation(registry, operation)
		allRealPublished := registry.Publications()
		realPublished := allRealPublished[beforeReal:]
		naivePublished, naiveErr := runOperationNaive(t, naive, operation)

		fmt.Fprintf(&log, "step=%02d input=%s client=%q now=%d", index+1, operation.kind, operation.clientID, operation.now)
		if operation.will != nil {
			fmt.Fprintf(&log, " will=%s/%s/D=%d", operation.will.Topic, operation.will.Payload, operation.will.DelayMillis)
		}
		if operation.kind == "disconnect" {
			fmt.Fprintf(&log, " normal=%t", operation.normal)
		}
		fmt.Fprintf(&log, " output_error=%v output_published=%#v decision=%s\n", realErr, realPublished, operation.reason)

		if !sameError(realErr, operation.wantErr) {
			t.Fatalf("step %d real error = %v, want %v\n%s", index+1, realErr, operation.wantErr, log.String())
		}
		if !sameError(naiveErr, operation.wantErr) {
			t.Fatalf("step %d naive error = %v, want %v\n%s", index+1, naiveErr, operation.wantErr, log.String())
		}
		if !publicationsEqual(realPublished, naivePublished) {
			t.Fatalf("step %d real publications = %#v, naive = %#v\n%s", index+1, realPublished, naivePublished, log.String())
		}
		if !publicationsEqual(registry.Publications(), naive.publications) {
			t.Fatalf("step %d accumulated real publications differ from naive\nreal=%#v\nnaive=%#v\n%s",
				index+1, registry.Publications(), naive.publications, log.String())
		}
	}

	t.Logf("deterministic input/output/decision log:\n%s", log.String())
	want := []Publication{
		{ClientID: "a", Topic: "will/a", Payload: []byte("A"), PublishedAt: 1501},
		{ClientID: "c", Topic: "will/c", Payload: []byte("C"), PublishedAt: 2100},
	}
	assertPublications(t, registry.Publications(), want)
}

func runOperation(registry *Registry, operation simulatedOperation) error {
	switch operation.kind {
	case "connect":
		return registry.Connect(operation.clientID, operation.keepAlive, operation.will, operation.now)
	case "activity":
		return registry.Activity(operation.clientID, operation.now)
	case "disconnect":
		return registry.Disconnect(operation.clientID, operation.normal, operation.now)
	case "advance":
		_, err := registry.Advance(operation.now)
		return err
	default:
		return fmt.Errorf("unknown operation %q", operation.kind)
	}
}

func runOperationNaive(t *testing.T, state *naiveRegistry, operation simulatedOperation) ([]Publication, error) {
	t.Helper()
	if state.hasNow && operation.now < state.now {
		return nil, ErrClockRewound
	}
	if operation.clientID == "" && operation.kind != "advance" {
		return nil, ErrEmptyClientID
	}
	if operation.keepAlive < 0 {
		return nil, ErrNegativeKeepAlive
	}
	if operation.will != nil && operation.will.DelayMillis < 0 {
		return nil, ErrNegativeDelay
	}

	state.hasNow = true
	state.now = operation.now
	published := state.advanceNaive()

	switch operation.kind {
	case "connect":
		_, online := state.connections[operation.clientID]
		delete(state.connections, operation.clientID)
		if online {
			delete(state.pending, operation.clientID)
		}
		state.connections[operation.clientID] = simulatedConnection{
			keepAlive: operation.keepAlive,
			lastSeen:  operation.now,
			will:      cloneWill(operation.will),
		}
	case "activity":
		if _, ok := state.connections[operation.clientID]; !ok {
			return published, ErrClientNotOnline
		}
		conn := state.connections[operation.clientID]
		conn.lastSeen = operation.now
		state.connections[operation.clientID] = conn
	case "disconnect":
		conn, ok := state.connections[operation.clientID]
		if !ok {
			return published, ErrClientNotOnline
		}
		delete(state.connections, operation.clientID)
		if !operation.normal && conn.will != nil {
			state.nextOrder++
			state.pending[operation.clientID] = simulatedPending{
				clientID: operation.clientID,
				will:     *cloneWill(conn.will),
				at:       operation.now + conn.will.DelayMillis,
				order:    state.nextOrder,
			}
		}
	case "advance":
		return published, nil
	}
	return published, nil
}

func (state *naiveRegistry) advanceNaive() []Publication {
	var clientIDs []string
	for clientID := range state.connections {
		clientIDs = append(clientIDs, clientID)
	}
	sortStrings(clientIDs)

	for _, clientID := range clientIDs {
		conn := state.connections[clientID]
		if conn.keepAlive == 0 || state.now-conn.lastSeen <= 1500*conn.keepAlive {
			continue
		}
		delete(state.connections, clientID)
		if conn.will != nil {
			state.nextOrder++
			state.pending[clientID] = simulatedPending{
				clientID: clientID,
				will:     *cloneWill(conn.will),
				at:       state.now + conn.will.DelayMillis,
				order:    state.nextOrder,
			}
		}
	}

	due := make([]simulatedPending, 0)
	for {
		var chosen *simulatedPending
		var chosenID string
		for clientID, pending := range state.pending {
			if pending.at > state.now {
				continue
			}
			if chosen == nil || pending.at < chosen.at || (pending.at == chosen.at && pending.order < chosen.order) {
				candidate := pending
				chosen = &candidate
				chosenID = clientID
			}
		}
		if chosen == nil {
			break
		}
		due = append(due, *chosen)
		delete(state.pending, chosenID)
	}

	before := len(state.publications)
	for _, pending := range due {
		state.publications = append(state.publications, Publication{
			ClientID:    pending.clientID,
			Topic:       pending.will.Topic,
			Payload:     cloneBytes(pending.will.Payload),
			PublishedAt: state.now,
		})
	}
	return clonePublications(state.publications[before:])
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func sameError(got error, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.Error() == want.Error()
}

func publicationsEqual(left []Publication, right []Publication) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ClientID != right[i].ClientID ||
			left[i].Topic != right[i].Topic ||
			string(left[i].Payload) != string(right[i].Payload) ||
			left[i].PublishedAt != right[i].PublishedAt {
			return false
		}
	}
	return true
}
