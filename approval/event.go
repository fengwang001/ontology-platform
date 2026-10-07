package approval

type Event struct {
	At       int
	Sequence int
	Kind     eventKind
	StageID  string
	Actor    string
	Payload  int
	Recorded int
}

type eventKind string

const (
	eventStart             eventKind = "start"
	eventPass              eventKind = "pass"
	eventFail              eventKind = "fail"
	eventRequestCorrection eventKind = "request_correction"
	eventSubmitCorrection  eventKind = "submit_correction"
	eventCorrectionExpired eventKind = "correction_expired"
	eventAutoPass          eventKind = "auto_pass"
	eventTerminate         eventKind = "terminate"
	eventWithdraw          eventKind = "withdraw"
)
