package imaging

import "sort"

// Snapshot 是可比较的完整业务状态（时钟、设备占用、留观事件、预约状态）。
type Snapshot struct {
	LastNow      int
	Devices      map[string]DeviceSnapshot
	Appointments map[string]AppointmentSnapshot
	ObsStarts    []int
	ObsEnds      []int
}

// DeviceSnapshot 朴素表示某设备的每日质控与全部占用区间（按开始排序）。
type DeviceSnapshot struct {
	Class         DeviceClass
	FieldStrength int
	QCs           []Interval
	Occupancies   []Interval
}

// AppointmentSnapshot 预约的可比较状态。
type AppointmentSnapshot struct {
	PatientID     string
	ExamTypeID    string
	DeviceID      string
	Start         int
	ExamEnd       int
	OccupEnd      int
	ObsEnd        int
	Status        AppointmentStatus
	HydrationAt   int
	HydrationDone bool
	PremedAt      int
	PremedDone    bool
}

// Snapshot 取主实现的确定性快照。
func (h *Hospital) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Snapshot{
		LastNow:      h.lastNow,
		Devices:      map[string]DeviceSnapshot{},
		Appointments: map[string]AppointmentSnapshot{},
		ObsStarts:    append([]int{}, h.obs.starts.keys()...),
		ObsEnds:      append([]int{}, h.obs.ends.keys()...),
	}
	for id, d := range h.devices {
		qcs := append([]Interval{}, d.qcs...)
		occ := append([]Interval{}, d.tree.intervals()...)
		sort.Slice(qcs, func(i, j int) bool {
			if qcs[i].Start != qcs[j].Start {
				return qcs[i].Start < qcs[j].Start
			}
			return qcs[i].End < qcs[j].End
		})
		s.Devices[id] = DeviceSnapshot{Class: d.class, FieldStrength: d.fieldStrength, QCs: qcs, Occupancies: occ}
	}
	for id, a := range h.appts {
		s.Appointments[id] = AppointmentSnapshot{
			PatientID: a.patientID, ExamTypeID: a.examTypeID, DeviceID: a.deviceID,
			Start: a.start, ExamEnd: a.examEnd, OccupEnd: a.occupEnd, ObsEnd: a.obsEnd,
			Status: a.status, HydrationAt: a.hydrationAt, HydrationDone: a.hydrationDone,
			PremedAt: a.premedAt, PremedDone: a.premedDone,
		}
	}
	return s
}
