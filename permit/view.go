package permit

func toStageView(def *StageDef, st *stageState) StageView {
	v := StageView{
		ID:         def.ID,
		Department: def.Department,
		Status:     st.status,
		StartDay:   st.startDay,
		Started:    st.started,
		DueDay:     st.dueDay,
		HasDue:     st.hasDue,
		Remaining:  st.remain,
		Overtime:   st.overtime,
		AutoPass:   def.AutoPass,
	}
	return v
}
