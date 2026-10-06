package imaging

import "sort"

// Snapshot 返回朴素模型的确定性快照，结构与主实现完全一致以便深比较。
func (n *NaiveModel) Snapshot() Snapshot {
	s := Snapshot{
		LastNow:      n.lastNow,
		Devices:      map[string]DeviceSnapshot{},
		Appointments: map[string]AppointmentSnapshot{},
	}
	idByDev := map[*naiveDevice]string{}
	for id, d := range n.devices {
		idByDev[d] = id
		occ := []Interval{}
		for _, o := range d.occ {
			occ = append(occ, o.iv)
		}
		qcs := append([]Interval{}, d.qcs...)
		sort.Slice(occ, func(i, j int) bool {
			if occ[i].Start != occ[j].Start {
				return occ[i].Start < occ[j].Start
			}
			return occ[i].End < occ[j].End
		})
		sort.Slice(qcs, func(i, j int) bool {
			if qcs[i].Start != qcs[j].Start {
				return qcs[i].Start < qcs[j].Start
			}
			return qcs[i].End < qcs[j].End
		})
		s.Devices[id] = DeviceSnapshot{Class: d.class, FieldStrength: d.fs, QCs: qcs, Occupancies: occ}
	}
	for id, a := range n.appts {
		s.Appointments[id] = AppointmentSnapshot{
			PatientID: a.patient, ExamTypeID: a.exam, DeviceID: a.device,
			Start: a.start, ExamEnd: a.examEnd, OccupEnd: a.end, ObsEnd: a.obsEnd,
			Status: a.status, HydrationAt: a.hydroAt, HydrationDone: a.hydro,
			PremedAt: a.premAt, PremedDone: a.prem,
		}
	}
	for _, o := range n.obs {
		s.ObsStarts = append(s.ObsStarts, o.s)
		s.ObsEnds = append(s.ObsEnds, o.e)
	}
	if s.ObsStarts == nil {
		s.ObsStarts = []int{}
	}
	if s.ObsEnds == nil {
		s.ObsEnds = []int{}
	}
	sort.Ints(s.ObsStarts)
	sort.Ints(s.ObsEnds)
	return s
}
