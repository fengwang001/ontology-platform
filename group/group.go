package group

import "sync/atomic"

// Group 是可嵌套任务组树的节点。
type Group struct {
	parent *Group
	cancel atomic.Bool
}

func New(parent *Group) *Group { return &Group{parent: parent} }

func (g *Group) Cancel()                    {}
func (g *Group) IsCancelled() bool          { return false }
func (g *Group) CancelledAncestor() *Group  { return nil }
func (g *Group) FailFast(err error) bool    { return false }
func (g *Group) FirstError() error          { return nil }
func (g *Group) Spawned() int64             { return 0 }
func (g *Group) Executed() int64            { return 0 }
func (g *Group) Skipped() int64             { return 0 }
func (g *Group) Failed() int64              { return 0 }
func (g *Group) Rejected() int64            { return 0 }
func (g *Group) LastCancelVisits() int64    { return 0 }
func (g *Group) LastProbeDepth() int64      { return 0 }
