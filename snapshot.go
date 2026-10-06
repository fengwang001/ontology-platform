package battery

type FaultReason string

const (
	FaultDeltaVoltage         FaultReason = "delta_voltage"
	FaultOvercurrentCharge    FaultReason = "overcurrent_charge"
	FaultOvercurrentDischarge FaultReason = "overcurrent_discharge"
)

type Snapshot struct {
	HasSample               bool
	TimeMS                  int64
	ChargeCurrentLimitMA    int64
	DischargeCurrentLimitMA int64
	ChargeProhibited        bool
	DischargeProhibited     bool
	BalancingRequested      bool
	SensorFault             bool
	Latched                 bool
	LatchReasons            []FaultReason
}

func cloneReasons(reasons []FaultReason) []FaultReason {
	if len(reasons) == 0 {
		return nil
	}
	cloned := make([]FaultReason, len(reasons))
	copy(cloned, reasons)
	return cloned
}
