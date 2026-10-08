package chat

import (
	"sync"
	"unicode/utf8"
)

// Channel parameter and argument bounds.
const (
	MaxNow        int64 = 1_000_000_000_000 // now is in [0, MaxNow] seconds
	MaxWindow     int64 = 86400             // E and R are in [1, MaxWindow]
	MaxEdits            = 10                // K is in [0, MaxEdits]
	MaxBodyLen          = 4000              // body length cap, in characters
	MaxMentions         = 20                // per-message mention cap
	MaxFetchLimit       = 100               // Fetch limit is in [1, MaxFetchLimit]
)

// member is the per-user channel state. The watermark only moves forward
// while the user stays joined; leaving and re-joining resets both marks.
type member struct {
	admin         bool
	watermark     int // read watermark: seqs <= watermark are read
	joinWatermark int // seqs <= joinWatermark are invisible to this member
}

// Channel is a single group-chat channel. All exported methods are safe for
// concurrent use; the internal mutex serializes every operation, so any
// concurrent execution is equivalent to some serial order.
type Channel struct {
	mu           sync.Mutex
	editWindow   int64 // E: edit allowed while now-sentAt < E
	recallWindow int64 // R: author recall allowed while now-sentAt < R
	maxEdits     int   // K: per-message edit cap

	lastNow  int64     // now of the last accepted operation
	messages []message // dense: seq = index+1, no holes ever
	members  map[string]*member

	// ownActive maps author -> seqs of their messages not yet recalled.
	ownActive map[string]*seqset
	// mentionIndex maps user -> seqs of non-recalled messages whose current
	// mention set contains the user.
	mentionIndex map[string]*seqset
	// recalledSeqs holds the seqs of all recalled messages.
	recalledSeqs *seqset
}

// NewChannel validates the channel parameters and returns an empty channel.
// The first user to Join becomes the channel admin.
func NewChannel(editWindow, recallWindow int64, maxEdits int) (*Channel, error) {
	if editWindow < 1 || editWindow > MaxWindow {
		return nil, reject("NewChannel", ErrInvalidParam, "edit window %d out of [1,%d]", editWindow, MaxWindow)
	}
	if recallWindow < 1 || recallWindow > MaxWindow {
		return nil, reject("NewChannel", ErrInvalidParam, "recall window %d out of [1,%d]", recallWindow, MaxWindow)
	}
	if maxEdits < 0 || maxEdits > MaxEdits {
		return nil, reject("NewChannel", ErrInvalidParam, "max edits %d out of [0,%d]", maxEdits, MaxEdits)
	}
	return &Channel{
		editWindow:   editWindow,
		recallWindow: recallWindow,
		maxEdits:     maxEdits,
		members:      make(map[string]*member),
		ownActive:    make(map[string]*seqset),
		mentionIndex: make(map[string]*seqset),
		recalledSeqs: &seqset{},
	}, nil
}

// Latest returns the latest assigned sequence number (0 when empty).
func (c *Channel) Latest() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func (c *Channel) ownSet(user string) *seqset {
	s := c.ownActive[user]
	if s == nil {
		s = &seqset{}
		c.ownActive[user] = s
	}
	return s
}

func (c *Channel) mentionSet(user string) *seqset {
	s := c.mentionIndex[user]
	if s == nil {
		s = &seqset{}
		c.mentionIndex[user] = s
	}
	return s
}

// --- shared validation helpers (called with c.mu held) ---

func validateUser(op, user string) *Error {
	if user == "" {
		return reject(op, ErrInvalidParam, "empty user id")
	}
	return nil
}

func validateNow(op string, now int64) *Error {
	if now < 0 || now > MaxNow {
		return reject(op, ErrInvalidParam, "now %d out of [0,%d]", now, MaxNow)
	}
	return nil
}

func validateBody(op, body string) *Error {
	if body == "" {
		return reject(op, ErrInvalidParam, "empty body")
	}
	if utf8.RuneCountInString(body) > MaxBodyLen {
		return reject(op, ErrInvalidParam, "body longer than %d characters", MaxBodyLen)
	}
	return nil
}

// checkMentionShape validates the mention-set invariants that do not depend
// on the message author: at most 20 distinct non-empty ids, all of them
// current members.
func (c *Channel) checkMentionShape(op string, mentions []string) *Error {
	if len(mentions) > MaxMentions {
		return reject(op, ErrInvalidParam, "%d mentions exceed %d", len(mentions), MaxMentions)
	}
	seen := make(map[string]struct{}, len(mentions))
	for _, id := range mentions {
		if id == "" {
			return reject(op, ErrInvalidParam, "empty mention id")
		}
		if _, dup := seen[id]; dup {
			return reject(op, ErrInvalidParam, "duplicate mention %q", id)
		}
		seen[id] = struct{}{}
		if _, ok := c.members[id]; !ok {
			return reject(op, ErrInvalidParam, "mention %q is not a member", id)
		}
	}
	return nil
}

// checkClock enforces monotonic time: now must not be smaller than the last
// accepted operation's now.
func (c *Channel) checkClock(op string, now int64) *Error {
	if now < c.lastNow {
		return reject(op, ErrClockSkew, "now %d < last accepted now %d", now, c.lastNow)
	}
	return nil
}

// requireMember returns the member entry or an ErrNotMember rejection.
func (c *Channel) requireMember(op, user string) (*member, *Error) {
	m, ok := c.members[user]
	if !ok {
		return nil, reject(op, ErrNotMember, "%q is not a member", user)
	}
	return m, nil
}

// --- membership operations ---

// Join adds user to the channel. The first joiner becomes admin. The
// watermark and the visibility floor are set to the current latest seq, so
// history predating the join is invisible and never counts as unread.
// Joining while already a member is an accepted no-op.
func (c *Channel) Join(user string, now int64) error {
	const op = "Join"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	if _, ok := c.members[user]; ok {
		c.lastNow = now
		return nil
	}
	latest := len(c.messages)
	c.members[user] = &member{
		admin:         len(c.members) == 0,
		watermark:     latest,
		joinWatermark: latest,
	}
	c.lastNow = now
	return nil
}

// Leave removes user from the channel, including any admin role. Re-joining
// later resets the watermark to the then-latest seq.
func (c *Channel) Leave(user string, now int64) error {
	const op = "Leave"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, user); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	if _, err := c.requireMember(op, user); err != nil {
		return err
	}
	delete(c.members, user)
	c.lastNow = now
	return nil
}

// Promote grants the admin role to target. The caller must be an admin;
// target must be a current member (checked as a parameter constraint).
// Promoting an existing admin is an accepted no-op.
func (c *Channel) Promote(admin, target string, now int64) error {
	const op = "Promote"
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateUser(op, admin); err != nil {
		return err
	}
	if err := validateUser(op, target); err != nil {
		return err
	}
	if err := validateNow(op, now); err != nil {
		return err
	}
	tm, ok := c.members[target]
	if !ok {
		return reject(op, ErrInvalidParam, "target %q is not a member", target)
	}
	if err := c.checkClock(op, now); err != nil {
		return err
	}
	am, err := c.requireMember(op, admin)
	if err != nil {
		return err
	}
	if !am.admin {
		return reject(op, ErrPermissionDenied, "%q is not an admin", admin)
	}
	tm.admin = true
	c.lastNow = now
	return nil
}
