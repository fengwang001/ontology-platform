package qc

type LevelSpec struct {
	Target            int64
	StandardDeviation int64
}

type AssaySpec struct {
	InstrumentID string
	AssayID      string
	Low          LevelSpec
	High         LevelSpec
	Validity     int64
}

type RunInput struct {
	InstrumentID string
	AssayID      string
	LowValue     int64
	HighValue    int64
}

type LevelResult struct {
	Deviation int64
}

type RunResult struct {
	Time      int64
	Low       LevelResult
	High      LevelResult
	Rules     []int
	Warning   bool
	Outage    bool
	Recovered bool
}

type ReportStatus string

const (
	ReportIssued   ReportStatus = "issued"
	ReportPending  ReportStatus = "pending_review"
	ReportReviewed ReportStatus = "reviewed"
)

type Report struct {
	ID           string
	InstrumentID string
	AssayID      string
	Time         int64
	Status       ReportStatus
}
