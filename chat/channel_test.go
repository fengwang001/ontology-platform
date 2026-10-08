package chat

import (
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func mustCode(t *testing.T, err error, want Code) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("rejection code = %v (%v), want %v", got, err, want)
	}
}

// newTestChannel returns a channel with E=10, R=5, K=2 and admin "alice".
func newTestChannel(t *testing.T) *Channel {
	t.Helper()
	c, err := NewChannel(10, 5, 2)
	mustOK(t, err)
	mustOK(t, c.Join("alice", 0))
	return c
}

func joinAll(t *testing.T, c *Channel, now int64, users ...string) {
	t.Helper()
	for _, u := range users {
		mustOK(t, c.Join(u, now))
	}
}

func send(t *testing.T, c *Channel, user, body string, mentions []string, now int64) int {
	t.Helper()
	seq, err := c.Send(user, body, mentions, now)
	mustOK(t, err)
	return seq
}

func sendErr(c *Channel, user, body string, mentions []string, now int64) error {
	_, err := c.Send(user, body, mentions, now)
	return err
}

func unread(t *testing.T, c *Channel, user string) int {
	t.Helper()
	n, err := c.Unread(user)
	mustOK(t, err)
	return n
}

func unreadMentions(t *testing.T, c *Channel, user string) int {
	t.Helper()
	n, err := c.UnreadMentions(user)
	mustOK(t, err)
	return n
}

func TestNewChannelParamValidation(t *testing.T) {
	if _, err := NewChannel(0, 5, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("E=0: got %v", err)
	}
	if _, err := NewChannel(86401, 5, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("E=86401: got %v", err)
	}
	if _, err := NewChannel(10, 0, 2); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("R=0: got %v", err)
	}
	if _, err := NewChannel(10, 5, 11); CodeOf(err) != ErrInvalidParam {
		t.Fatalf("K=11: got %v", err)
	}
	if _, err := NewChannel(1, 86400, 10); err != nil {
		t.Fatalf("boundary params should pass: %v", err)
	}
}
