package ontology

import (
	"errors"
	"testing"
)

func TestKeepAliveBoundary(t *testing.T) {
	registry := NewRegistry()
	will := &Will{Topic: "will/a", Payload: []byte("bye"), DelayMillis: 1000}

	if err := registry.Connect("a", 2, will, 0); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, err := registry.Advance(3000); err != nil {
		t.Fatalf("Advance at exact keep-alive error = %v", err)
	}
	if publications := registry.Publications(); len(publications) != 0 {
		t.Fatalf("publications at exact keep-alive = %#v", publications)
	}

	if _, err := registry.Advance(3001); err != nil {
		t.Fatalf("Advance one millisecond after boundary error = %v", err)
	}
	if err := registry.Activity("a", 3002); !errors.Is(err, ErrClientNotOnline) {
		t.Fatalf("Activity after timeout error = %v, want %v", err, ErrClientNotOnline)
	}
	if publications := waitPublicationCount(t, registry, 0); len(publications) != 0 {
		t.Fatal("unexpected publication before will delay elapsed")
	}
	if _, err := registry.Advance(4001); err != nil {
		t.Fatalf("Advance to scheduled publication error = %v", err)
	}

	publications := waitPublicationCount(t, registry, 1)
	assertPublication(t, publications[0], Publication{
		ClientID:    "a",
		Topic:       "will/a",
		Payload:     []byte("bye"),
		PublishedAt: 4001,
	})
}

func TestWillLifecyclePaths(t *testing.T) {
	t.Run("zero delay publishes in timeout processing", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "z", 1, &Will{Topic: "will/z", Payload: []byte("z"), DelayMillis: 0}, 0)

		published, err := registry.Advance(1501)
		if err != nil {
			t.Fatalf("Advance() error = %v", err)
		}
		if len(published) != 1 {
			t.Fatalf("Advance() publications = %#v, want one immediate publication", published)
		}
		assertPublication(t, published[0], Publication{
			ClientID: "z", Topic: "will/z", Payload: []byte("z"), PublishedAt: 1501,
		})
	})

	t.Run("takeover invalidates attached and pending wills", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "c", 0, &Will{Topic: "old", Payload: []byte("old"), DelayMillis: 1000}, 0)
		if err := registry.Disconnect("c", false, 100); err != nil {
			t.Fatalf("abnormal Disconnect() error = %v", err)
		}
		mustConnect(t, registry, "c", 0, &Will{Topic: "reconnected", Payload: []byte("reconnected"), DelayMillis: 1000}, 200)
		mustConnect(t, registry, "c", 0, &Will{Topic: "new", Payload: []byte("new"), DelayMillis: 1000}, 300)
		if err := registry.Disconnect("c", true, 400); err != nil {
			t.Fatalf("normal Disconnect() error = %v", err)
		}
		if _, err := registry.Advance(2200); err != nil {
			t.Fatalf("Advance() error = %v", err)
		}
		if publications := registry.Publications(); len(publications) != 0 {
			t.Fatalf("publications after takeover and normal disconnect = %#v", publications)
		}
	})

	t.Run("reconnect exactly at publication time cannot cancel", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "d", 0, &Will{Topic: "will/d", Payload: []byte("d"), DelayMillis: 1000}, 0)
		if err := registry.Disconnect("d", false, 0); err != nil {
			t.Fatalf("abnormal Disconnect() error = %v", err)
		}
		mustConnect(t, registry, "d", 0, &Will{Topic: "will/d2", Payload: []byte("d2"), DelayMillis: 5000}, 1000)
		assertPublications(t, registry.Publications(), []Publication{
			{ClientID: "d", Topic: "will/d", Payload: []byte("d"), PublishedAt: 1000},
		})
	})

	t.Run("normal disconnect does not publish", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "n", 0, &Will{Topic: "will/n", Payload: []byte("n"), DelayMillis: 0}, 0)
		if err := registry.Disconnect("n", true, 10); err != nil {
			t.Fatalf("normal Disconnect() error = %v", err)
		}
		if _, err := registry.Advance(10); err != nil {
			t.Fatalf("Advance() error = %v", err)
		}
		if publications := registry.Publications(); len(publications) != 0 {
			t.Fatalf("normal disconnect publications = %#v", publications)
		}
	})
}

func TestRejectionRules(t *testing.T) {
	t.Run("clock rewind and invalid parameters do not mutate state", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "clock", 0, nil, 10)

		if err := registry.Connect("", -1, &Will{DelayMillis: -1}, 9); !errors.Is(err, ErrClockRewound) {
			t.Fatalf("clock rewind priority error = %v, want %v", err, ErrClockRewound)
		}
		if err := registry.Connect("", -1, &Will{DelayMillis: -1}, 10); !errors.Is(err, ErrEmptyClientID) {
			t.Fatalf("empty id priority error = %v, want %v", err, ErrEmptyClientID)
		}
		if err := registry.Connect("bad", -1, &Will{DelayMillis: -1}, 10); !errors.Is(err, ErrNegativeKeepAlive) {
			t.Fatalf("negative keep-alive priority error = %v, want %v", err, ErrNegativeKeepAlive)
		}
		if err := registry.Connect("bad", 0, &Will{DelayMillis: -1}, 10); !errors.Is(err, ErrNegativeDelay) {
			t.Fatalf("negative delay error = %v, want %v", err, ErrNegativeDelay)
		}
		if _, err := registry.Advance(9); !errors.Is(err, ErrClockRewound) {
			t.Fatalf("Advance clock rewind error = %v, want %v", err, ErrClockRewound)
		}
		if err := registry.Activity("clock", 1511); err != nil {
			t.Fatalf("valid advancement after rejected calls error = %v", err)
		}
		if publications := registry.Publications(); len(publications) != 0 {
			t.Fatalf("publications after rejected calls = %#v", publications)
		}
	})

	t.Run("offline operation keeps entry timeout and publication", func(t *testing.T) {
		registry := NewRegistry()
		mustConnect(t, registry, "timed-out", 1, &Will{
			Topic: "will/timed-out", Payload: []byte("bye"), DelayMillis: 0,
		}, 0)
		mustConnect(t, registry, "caller", 0, nil, 0)

		err := registry.Activity("timed-out", 1501)
		if !errors.Is(err, ErrClientNotOnline) {
			t.Fatalf("Activity after entry timeout error = %v, want %v", err, ErrClientNotOnline)
		}
		if err := registry.Disconnect("caller", false, 1501); err != nil {
			t.Fatalf("caller remains online after independent timeout = %v", err)
		}
		assertPublications(t, registry.Publications(), []Publication{
			{ClientID: "timed-out", Topic: "will/timed-out", Payload: []byte("bye"), PublishedAt: 1501},
		})
	})
}

func mustConnect(t *testing.T, registry *Registry, clientID string, keepAliveSeconds int64, will *Will, now int64) {
	t.Helper()
	if err := registry.Connect(clientID, keepAliveSeconds, will, now); err != nil {
		t.Fatalf("Connect(%q, %d, %v) error = %v", clientID, now, will, err)
	}
}

func assertPublications(t *testing.T, got []Publication, want []Publication) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("publications = %#v, want %#v", got, want)
	}
	for i := range want {
		assertPublication(t, got[i], want[i])
	}
}

func waitPublicationCount(t *testing.T, registry *Registry, want int) []Publication {
	t.Helper()
	publications := registry.Publications()
	if len(publications) != want {
		t.Fatalf("publication count = %d, want %d (%#v)", len(publications), want, publications)
	}
	return publications
}

func assertPublication(t *testing.T, got Publication, want Publication) {
	t.Helper()
	if got.ClientID != want.ClientID || got.Topic != want.Topic ||
		string(got.Payload) != string(want.Payload) || got.PublishedAt != want.PublishedAt {
		t.Fatalf("publication = %#v, want %#v", got, want)
	}
}
