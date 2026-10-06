package floorcontrol_test

import (
	"errors"
	"fmt"
	"strings"

	fc "ontology/floorcontrol"
)

// 规范的拒绝类别（错误只按类别比较，避免跨模型依赖具体变量）。
var (
	errInvalid      = errors.New("invalid argument")
	errClock        = errors.New("clock back")
	errClosed       = errors.New("closed")
	errNoOperator   = errors.New("operator not in room")
	errPerm         = errors.New("permission denied")
	errNoTarget     = errors.New("target not found")
	errAlreadyIn    = errors.New("already in room")
	errInQueue      = errors.New("already raised")
	errSpeaking     = errors.New("already speaker")
	errMutedRaise   = errors.New("raised while muted")
	errFull         = errors.New("queue full")
	errHasSpeaker   = errors.New("speaker active")
	errEmpty        = errors.New("queue empty")
	errNotQueued    = errors.New("not in queue")
	errNotSpeaking  = errors.New("not speaker")
	errTargetHost   = errors.New("target is host")
	errRoleSame     = errors.New("role unchanged")
	errAlreadyMuted = errors.New("already muted")
	errNotMuted     = errors.New("not muted")
	errOK           = errors.New("ok")
)

func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fc.ErrInvalidArgument):
		return errInvalid
	case errors.Is(err, fc.ErrClockBack):
		return errClock
	case errors.Is(err, fc.ErrClosed):
		return errClosed
	case errors.Is(err, fc.ErrOperatorNotInRoom):
		return errNoOperator
	case errors.Is(err, fc.ErrPermissionDenied):
		return errPerm
	case errors.Is(err, fc.ErrTargetNotFound):
		return errNoTarget
	case errors.Is(err, fc.ErrAlreadyInRoom):
		return errAlreadyIn
	case errors.Is(err, fc.ErrAlreadyRaised):
		return errInQueue
	case errors.Is(err, fc.ErrAlreadySpeaker):
		return errSpeaking
	case errors.Is(err, fc.ErrRaisedWhileMuted):
		return errMutedRaise
	case errors.Is(err, fc.ErrQueueFull):
		return errFull
	case errors.Is(err, fc.ErrSpeakerActive):
		return errHasSpeaker
	case errors.Is(err, fc.ErrQueueEmpty):
		return errEmpty
	case errors.Is(err, fc.ErrNotInQueue):
		return errNotQueued
	case errors.Is(err, fc.ErrNotSpeaker):
		return errNotSpeaking
	case errors.Is(err, fc.ErrIsHost):
		return errTargetHost
	case errors.Is(err, fc.ErrRoleUnchanged):
		return errRoleSame
	case errors.Is(err, fc.ErrAlreadyMuted):
		return errAlreadyMuted
	case errors.Is(err, fc.ErrNotMuted):
		return errNotMuted
	default:
		return fmt.Errorf("unmapped error: %w", err)
	}
}

// Op 是一条对两个模型等价下发的操作。
type Op struct {
	Kind string
	A    string // user / operator
	B    string // target（可选）
	Now  int64
}

func (o Op) String() string {
	if o.B == "" {
		return fmt.Sprintf("%s(%q, t=%d)", o.Kind, o.A, o.Now)
	}
	return fmt.Sprintf("%s(%q, %q, t=%d)", o.Kind, o.A, o.B, o.Now)
}

type operator struct{ user string }

type outcome struct {
	err  error // 类别
	pos  int
	snap stateView
}

type stateView struct {
	closed    bool
	host      string
	speaker   string
	remaining int64
	queue     string
	roles     string
	mutes     string
}

func roleName(r fc.Role) string {
	switch r {
	case fc.RoleHost:
		return "H"
	case fc.RoleCoManager:
		return "C"
	default:
		return "A"
	}
}

func snapshotOf(s fc.Snapshot) stateView {
	v := stateView{
		host:      s.Host,
		speaker:   s.Speaker,
		remaining: s.Remaining,
		queue:     strings.Join(s.Queue, ","),
	}
	var roles, mutes []string
	for _, m := range s.Members {
		roles = append(roles, m.User+":"+roleName(m.Role))
		if m.Muted {
			mutes = append(mutes, m.User)
		}
	}
	v.roles = strings.Join(roles, ",")
	v.mutes = strings.Join(mutes, ",")
	return v
}

func naiveRoleName(r naiveRole) string {
	switch r {
	case nHost:
		return "H"
	case nCoManager:
		return "C"
	default:
		return "A"
	}
}

func naiveView(s naiveSnapshot) stateView {
	v := stateView{
		host:      s.host,
		speaker:   s.speaker,
		remaining: s.remaining,
		queue:     strings.Join(s.queue, ","),
	}
	var roles, mutes []string
	for i, m := range s.members {
		roles = append(roles, s.joinSeq[i]+":"+naiveRoleName(m.role))
		if m.muted {
			mutes = append(mutes, s.joinSeq[i])
		}
	}
	v.roles = strings.Join(roles, ",")
	v.mutes = strings.Join(mutes, ",")
	return v
}

// runOp 在生产模型上执行一条操作。
func runReal(room *fc.Room, o Op) outcome {
	var out outcome
	var err error
	switch o.Kind {
	case "Join":
		err = room.Join(o.A, o.Now)
	case "Leave":
		err = room.Leave(o.A, o.Now)
	case "Raise":
		err = room.Raise(o.A, o.Now)
	case "Lower":
		err = room.Lower(o.A, o.Now)
	case "Appoint":
		err = room.Appoint(o.A, o.B, o.Now)
	case "Dismiss":
		err = room.Dismiss(o.A, o.B, o.Now)
	case "Grant":
		err = room.Grant(o.A, o.Now)
	case "Yield":
		err = room.Yield(o.A, o.Now)
	case "Mute":
		err = room.Mute(o.A, o.B, o.Now)
	case "Unmute":
		err = room.Unmute(o.A, o.B, o.Now)
	case "Snapshot":
		s, e := room.Snapshot(o.Now)
		err = e
		if e == nil {
			out.snap = snapshotOf(s)
		}
	case "QueuePos":
		p, e := room.QueuePos(o.A, o.Now)
		err = e
		out.pos = p
	default:
		panic("unknown op " + o.Kind)
	}
	out.err = classify(err)
	return out
}

// runNaive 在朴素模型上执行同一条操作。
func runNaive(n *naiveRoom, o Op) outcome {
	var out outcome
	var err error
	switch o.Kind {
	case "Join":
		err = n.join(o.A, o.Now)
	case "Leave":
		err = n.leave(o.A, o.Now)
	case "Raise":
		err = n.raise(o.A, o.Now)
	case "Lower":
		err = n.lower(o.A, o.Now)
	case "Appoint":
		err = n.roleChange(o.A, o.B, o.Now, true)
	case "Dismiss":
		err = n.roleChange(o.A, o.B, o.Now, false)
	case "Grant":
		err = n.grant(o.A, o.Now)
	case "Yield":
		err = n.yield(o.A, o.Now)
	case "Mute":
		err = n.mute(o.A, o.B, o.Now, true)
	case "Unmute":
		err = n.mute(o.A, o.B, o.Now, false)
	case "Snapshot":
		s, e := n.snapshot(o.Now)
		err = e
		if e == nil {
			out.snap = naiveView(s)
		}
	case "QueuePos":
		p, e := n.queuePos(o.A, o.Now)
		err = e
		out.pos = p
	default:
		panic("unknown op " + o.Kind)
	}
	out.err = err
	return out
}

func outcomesEqual(a, b outcome) (string, bool) {
	if errName(a.err) != errName(b.err) {
		return fmt.Sprintf("error mismatch: real=%v naive=%v", a.err, b.err), false
	}
	if a.pos != b.pos {
		return fmt.Sprintf("pos mismatch: real=%d naive=%d", a.pos, b.pos), false
	}
	if a.err == nil && (oKindView(a.snap) != oKindView(b.snap)) {
		return fmt.Sprintf("state mismatch:\n real=%+v\nnaive=%+v", a.snap, b.snap), false
	}
	return "", true
}

func errName(e error) string {
	if e == nil {
		return "OK"
	}
	return e.Error()
}

func oKindView(v stateView) string {
	return fmt.Sprintf("host=%s|sp=%s|rem=%d|q=[%s]|roles=[%s]|mutes=[%s]",
		v.host, v.speaker, v.remaining, v.queue, v.roles, v.mutes)
}
