package chainreplication

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestLoggingShowsInputsOutputsDecisions verifies the log records inputs,
// outputs and the rationale for each decision (reject, discard, commit,
// outcome).
func TestLoggingShowsInputsOutputsDecisions(t *testing.T) {
	var buf bytes.Buffer
	net := NewQueueNetwork(nil)
	c, err := NewCoordinator([]string{"H", "T"}, net,
		WithLogger(log.New(&buf, "", 0)),
		WithLogWriter(&buf),
	)
	if err != nil {
		t.Fatal(err)
	}
	net.Bind(c.Deliver)

	if _, err := c.Write("H", "w"); err != nil {
		t.Fatal(err)
	}
	net.Deliver(0) // H->T
	if _, err := c.Read("H"); err == nil {
		t.Fatal("read at non-tail must be rejected")
	}
	net.Send(Message{Kind: KindWrite, From: "H", To: "ghost", Seq: 9})
	net.Deliver(len(net.Pending()) - 1)
	if err := c.Fail("H"); err != nil {
		t.Fatal(err)
	}

	logText := buf.String()
	for _, want := range []string{
		"INPUT Write",
		"OUTPUT seq=1",
		"DECIDE commit tail=T",
		"DECIDE OUTCOME seq=1 committed=true",
		"REJECT not-tail",
		"DISCARD dead/unknown endpoint",
		"DECIDE head-failure",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, logText)
		}
	}
}
