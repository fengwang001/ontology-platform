package recovery

import "fmt"

// snapshotView 是快照逐条校验后的视图。
// status 只包含快照中实际存在的记录（valid/corrupt）；
// 不在其中的对象按“不存在（absent）”处理，与损坏严格区分。
type snapshotView struct {
	version Version
	status  map[ObjectID]ObjectStatus
	state   map[ObjectID]State
	corrupt []ObjectID
}

// logView 是动作日志逐条校验后的视图。
// actions 只保留完整未损坏的动作；corrupt 列出被整条丢弃的动作 ID。
type logView struct {
	base    Version
	actions []Action
	corrupt []ActionID
}

// verifySnapshot 逐条校验快照记录：
// 校验失败只把该对象标记为“快照时刻状态不可读”，不影响其他对象。
func verifySnapshot(snap *Snapshot) *snapshotView {
	v := &snapshotView{
		version: snap.Version,
		status:  make(map[ObjectID]ObjectStatus, len(snap.Records)),
		state:   make(map[ObjectID]State, len(snap.Records)),
	}
	for _, rec := range snap.Records {
		want := SnapshotChecksum(rec.ObjectID, rec.State)
		if rec.Checksum == "" || rec.Checksum != want {
			v.status[rec.ObjectID] = StatusCorrupt
			v.corrupt = append(v.corrupt, rec.ObjectID)
			continue
		}
		v.status[rec.ObjectID] = StatusValid
		v.state[rec.ObjectID] = rec.State
	}
	return v
}

// verifyActions 逐条校验动作记录：
// 任一字段不可解析或校验失败，整条动作视为不可用，
// 不保留其中任何“看起来可解析”的部分字段。
func verifyActions(lg *Log) *logView {
	v := &logView{base: lg.Base}
	for _, rec := range lg.Records {
		if rec.ActionID == "" || rec.Effects == nil {
			v.corrupt = append(v.corrupt, rec.ActionID)
			continue
		}
		want := ActionChecksum(rec.ActionID, rec.Base, rec.Effects)
		if rec.Checksum == "" || rec.Checksum != want {
			v.corrupt = append(v.corrupt, rec.ActionID)
			continue
		}
		v.actions = append(v.actions, Action{
			ActionID: rec.ActionID,
			Base:     rec.Base,
			Effects:  rec.Effects,
		})
	}
	return v
}

func objectsOf(actions []Action) map[ObjectID]struct{} {
	set := make(map[ObjectID]struct{})
	for _, a := range actions {
		for obj := range a.Effects {
			set[obj] = struct{}{}
		}
	}
	return set
}

func formatIDs[T ~string](ids []T) string {
	return fmt.Sprint(ids)
}
