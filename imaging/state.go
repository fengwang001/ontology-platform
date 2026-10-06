package imaging

// AppointmentStatus 预约状态。
type AppointmentStatus int

const (
	// StatusBooked 已受理、未签到。
	StatusBooked AppointmentStatus = 1
	// StatusCheckedIn 已签到。
	StatusCheckedIn AppointmentStatus = 2
	// StatusNeedsReschedule 签到复核失败，需改期（设备与留观占用已释放）。
	StatusNeedsReschedule AppointmentStatus = 3
	// StatusCancelled 已取消。
	StatusCancelled AppointmentStatus = 4
)

type device struct {
	id            string
	class         DeviceClass
	fieldStrength int
	qcs           []Interval // 日内 [start,end)，0 <= start < end <= 1440
	tree          *intervalTree
}

type examType struct {
	id       string
	class    DeviceClass
	duration int
	enhanced bool
}

type kidneyResult struct {
	value     int
	sampledAt int
	order     int // 全院登记次序，并列采样时刻时认登记在后者
}

type patient struct {
	id               string
	highRisk         bool
	noImplant        bool
	maxFieldStrength int
	allergic         bool
	latest           kidneyResult
}

type appointment struct {
	id            string
	patientID     string
	examTypeID    string
	deviceID      string
	start         int
	examEnd       int // start + 占用时长
	occupEnd      int // examEnd + 清洁时长
	obsEnd        int // examEnd + 留观时长（仅增强，已计入留观结构）
	hydrationAt   int
	hydrationDone bool
	premedAt      int
	premedDone    bool
	status        AppointmentStatus
	treeID        uint64
}

// 留观位：以开始/结束两个时刻多重集合表示半开区间 [examEnd, obsEnd)。
type observationCounter struct {
	starts *multiSet
	ends   *multiSet
}
