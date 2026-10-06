package battery

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type naiveModel struct {
	cfg              Config
	samples          []Sample
	chargeStoppedAt  []bool
	dischargeStopAt  []bool
	firstLatchIndex  int
	latched          bool
	acceptedAfter    bool
	reasons          []FaultReason
	chargeStopped    bool
	dischargeStopped bool
	last             Snapshot
}

func (m *naiveModel) runStart(condition func(Sample) bool, sampleIndex int) int64 {
	start := int64(-1)
	for index := sampleIndex; index >= 0; index-- {
		if !condition(m.samples[index]) {
			break
		}
		start = m.samples[index].TimeMS
	}
	return start
}

func (m *naiveModel) sustained(condition func(Sample) bool, sampleIndex int, duration int64) bool {
	start := m.runStart(condition, sampleIndex)
	return start >= 0 && m.samples[sampleIndex].TimeMS-start >= duration
}

func naiveExtrema(sample Sample) (int64, int64) {
	minimum := sample.VoltagesMV[0]
	maximum := sample.VoltagesMV[0]
	for _, voltage := range sample.VoltagesMV[1:] {
		if voltage < minimum {
			minimum = voltage
		}
		if voltage > maximum {
			maximum = voltage
		}
	}
	return minimum, maximum
}

func (m *naiveModel) limitsAt(sample Sample, chargeStopped, dischargeStopped bool) currentLimits {
	valid := make([]int64, 0, len(sample.Temperatures))
	for _, temperature := range sample.Temperatures {
		if temperature.Valid {
			valid = append(valid, temperature.ValueDeciC)
		}
	}
	if len(valid) == 0 {
		return currentLimits{}
	}
	minimumVoltage, maximumVoltage := naiveExtrema(sample)
	minimumTemperature := valid[0]
	maximumTemperature := valid[0]
	for _, value := range valid[1:] {
		if value < minimumTemperature {
			minimumTemperature = value
		}
		if value > maximumTemperature {
			maximumTemperature = value
		}
	}
	charge := scaleCurrent(m.cfg.RatedChargeCurrentMA, lookupPermille(m.cfg.ChargeVoltageTable, maximumVoltage))
	charge = minInt64(charge, scaleCurrent(m.cfg.RatedChargeCurrentMA, lookupPermille(m.cfg.ChargeTemperatureTable, minimumTemperature)))
	charge = minInt64(charge, scaleCurrent(m.cfg.RatedChargeCurrentMA, lookupPermille(m.cfg.ChargeTemperatureTable, maximumTemperature)))
	discharge := scaleCurrent(m.cfg.RatedDischargeCurrentMA, lookupPermille(m.cfg.DischargeVoltageTable, minimumVoltage))
	discharge = minInt64(discharge, scaleCurrent(m.cfg.RatedDischargeCurrentMA, lookupPermille(m.cfg.DischargeTemperatureTable, minimumTemperature)))
	discharge = minInt64(discharge, scaleCurrent(m.cfg.RatedDischargeCurrentMA, lookupPermille(m.cfg.DischargeTemperatureTable, maximumTemperature)))
	if len(valid) < len(sample.Temperatures) {
		charge /= 2
		discharge /= 2
	}
	if chargeStopped {
		charge = 0
	}
	if dischargeStopped {
		discharge = 0
	}
	return currentLimits{charge: charge, discharge: discharge}
}

func (m *naiveModel) addReason(reason FaultReason) {
	for _, existing := range m.reasons {
		if existing == reason {
			return
		}
	}
	m.reasons = append(m.reasons, reason)
	m.latched = true
	m.acceptedAfter = false
}

func (m *naiveModel) submit(sample Sample) Snapshot {
	m.samples = append(m.samples, sample)
	index := len(m.samples) - 1
	minimumVoltage, maximumVoltage := naiveExtrema(sample)
	overEntry := func(candidate Sample) bool {
		_, high := naiveExtrema(candidate)
		return high >= m.cfg.OvervoltageThresholdMV
	}
	overClear := func(candidate Sample) bool {
		_, high := naiveExtrema(candidate)
		return high < m.cfg.OvervoltageThresholdMV-m.cfg.RecoveryHysteresisMV
	}
	underEntry := func(candidate Sample) bool {
		low, _ := naiveExtrema(candidate)
		return low <= m.cfg.UndervoltageThresholdMV
	}
	underClear := func(candidate Sample) bool {
		low, _ := naiveExtrema(candidate)
		return low > m.cfg.UndervoltageThresholdMV+m.cfg.RecoveryHysteresisMV
	}
	if !m.chargeStopped && m.sustained(overEntry, index, m.cfg.ConfirmationDurationMS) {
		m.chargeStopped = true
	} else if m.chargeStopped && m.sustained(overClear, index, m.cfg.ConfirmationDurationMS) {
		m.chargeStopped = false
	}
	if !m.dischargeStopped && m.sustained(underEntry, index, m.cfg.ConfirmationDurationMS) {
		m.dischargeStopped = true
	} else if m.dischargeStopped && m.sustained(underClear, index, m.cfg.ConfirmationDurationMS) {
		m.dischargeStopped = false
	}
	m.chargeStoppedAt = append(m.chargeStoppedAt, m.chargeStopped)
	m.dischargeStopAt = append(m.dischargeStopAt, m.dischargeStopped)

	limits := m.limitsAt(sample, m.chargeStopped, m.dischargeStopped)
	delta := maximumVoltage-minimumVoltage > m.cfg.DeltaVoltageLimitMV
	wasLatched := m.latched
	deltaStart := int64(-1)
	for historical := index; historical >= 0; historical-- {
		candidate := m.samples[historical]
		low, high := naiveExtrema(candidate)
		if high-low <= m.cfg.DeltaVoltageLimitMV ||
			candidate.CurrentMA <= m.cfg.IdleCurrentThresholdMA {
			break
		}
		deltaStart = candidate.TimeMS
	}
	if deltaStart >= 0 && sample.TimeMS-deltaStart >= m.cfg.ConfirmationDurationMS {
		m.addReason(FaultDeltaVoltage)
	}

	var magnitude, allowed int64
	var reason FaultReason
	if sample.CurrentMA > 0 {
		magnitude, allowed, reason = sample.CurrentMA, limits.charge, FaultOvercurrentCharge
	} else if sample.CurrentMA < 0 {
		magnitude, allowed, reason = -sample.CurrentMA, limits.discharge, FaultOvercurrentDischarge
	}
	excess := magnitude - allowed
	if excess > 0 {
		start := int64(-1)
		for historical := index; historical >= 0; historical-- {
			candidate := m.samples[historical]
			candidateLimits := m.limitsAt(candidate,
				m.chargeStoppedAt[historical], m.dischargeStopAt[historical])
			if m.firstLatchIndex >= 0 && historical > m.firstLatchIndex {
				candidateLimits = currentLimits{}
			}
			var candidateMagnitude, candidateAllowed int64
			if candidate.CurrentMA > 0 && reason == FaultOvercurrentCharge {
				candidateMagnitude, candidateAllowed = candidate.CurrentMA, candidateLimits.charge
			} else if candidate.CurrentMA < 0 && reason == FaultOvercurrentDischarge {
				candidateMagnitude, candidateAllowed = -candidate.CurrentMA, candidateLimits.discharge
			}
			if candidateMagnitude-candidateAllowed <= 0 {
				break
			}
			start = candidate.TimeMS
		}
		if start >= 0 {
			tier := m.cfg.OvercurrentTiers[0]
			for _, candidate := range m.cfg.OvercurrentTiers[1:] {
				if excess >= candidate.ExcessMA {
					tier = candidate
				}
			}
			if sample.TimeMS-start >= tier.ToleranceMS {
				m.addReason(reason)
			}
		}
	}

	sensorFault := true
	for _, reading := range sample.Temperatures {
		if reading.Valid {
			sensorFault = false
		}
	}
	if m.latched {
		limits = currentLimits{}
		m.acceptedAfter = true
	}
	if !wasLatched && m.latched {
		m.firstLatchIndex = index
	}
	m.last = Snapshot{
		HasSample:               true,
		TimeMS:                  sample.TimeMS,
		ChargeCurrentLimitMA:    limits.charge,
		DischargeCurrentLimitMA: limits.discharge,
		ChargeProhibited:        m.chargeStopped,
		DischargeProhibited:     m.dischargeStopped,
		BalancingRequested:      delta,
		SensorFault:             sensorFault,
		Latched:                 m.latched,
		LatchReasons:            cloneReasons(m.reasons),
	}
	return m.last
}

func randomConfig() Config {
	voltageTable := LimitTable{
		Boundaries: []int64{0, 2800, 3200},
		Permilles:  []int{100, 500, 1000},
	}
	temperatureTable := LimitTable{
		Boundaries: []int64{-1 << 63, -100, 100},
		Permilles:  []int{100, 500, 1000},
	}
	return Config{
		CellCount:                 3,
		TemperatureCount:          4,
		ChargeVoltageTable:        voltageTable,
		DischargeVoltageTable:     voltageTable,
		ChargeTemperatureTable:    temperatureTable,
		DischargeTemperatureTable: temperatureTable,
		RatedChargeCurrentMA:      1000,
		RatedDischargeCurrentMA:   2000,
		OvervoltageThresholdMV:    4200,
		UndervoltageThresholdMV:   2800,
		DeltaVoltageLimitMV:       100,
		RecoveryHysteresisMV:      10,
		ConfirmationDurationMS:    3,
		IdleCurrentThresholdMA:    50,
		OvercurrentTiers: [3]OvercurrentTier{
			{ExcessMA: 1, ToleranceMS: 20},
			{ExcessMA: 100, ToleranceMS: 10},
			{ExcessMA: 400, ToleranceMS: 0},
		},
	}
}

func TestRandomNaiveModelComparison(t *testing.T) {
	for seed := int64(0); seed < 64; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := randomConfig()
		manager, err := NewManager(cfg)
		if err != nil {
			t.Fatal(err)
		}
		naive := &naiveModel{cfg: cfg, firstLatchIndex: -1}
		timeMS := int64(0)
		for step := 0; step < 120; step++ {
			timeMS += int64(rng.Intn(4))
			voltages := []int64{
				int64(2700 + rng.Intn(1600)),
				int64(2700 + rng.Intn(1600)),
				int64(2700 + rng.Intn(1600)),
			}
			temperatures := make([]TemperatureReading, 4)
			for index := range temperatures {
				temperatures[index] = TemperatureReading{
					Valid:      rng.Intn(10) > 1,
					ValueDeciC: int64(-150 + rng.Intn(450)),
				}
			}
			currentChoices := []int64{0, 20, -20, 50, -50, 51, -51, 450, -450, 501, -501, 901, -901, 1001, -2001}
			sample := Sample{
				TimeMS:       timeMS,
				VoltagesMV:   voltages,
				Temperatures: temperatures,
				CurrentMA:    currentChoices[rng.Intn(len(currentChoices))],
			}
			got, submitErr := manager.Submit(sample)
			if submitErr != nil {
				t.Logf("seed=%d step=%d rejected input=%+v error=%v", seed, step, sample, submitErr)
				continue
			}
			want := naive.submit(sample)
			if testing.Verbose() {
				t.Logf("seed=%d step=%d input=%+v output=%+v reasons=%v overVolt=%d underVolt=%d",
					seed, step, sample, got, got.LatchReasons,
					maxSampleVoltage(sample), minSampleVoltage(sample))
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot mismatch\ninput=%+v\ngot=%+v\nwant=%+v", sample, got, want)
			}
		}
		t.Logf("seed=%d completed %d accepted/rejected samples and matched naive replay", seed, 120)
	}
}

func TestConcurrentSubmitsAndSnapshotReads(t *testing.T) {
	manager, err := NewManager(randomConfig())
	if err != nil {
		t.Fatal(err)
	}
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			timeMS := int64(worker + 1)
			for step := 0; step < 50; step++ {
				sample := Sample{
					TimeMS:       timeMS + int64(step*4),
					VoltagesMV:   []int64{3500, 3500, 3500},
					Temperatures: []TemperatureReading{{Valid: true, ValueDeciC: 50}, {Valid: true, ValueDeciC: 50}, {Valid: true, ValueDeciC: 50}, {Valid: true, ValueDeciC: 50}},
					CurrentMA:    0,
				}
				_, _ = manager.Submit(sample)
			}
		}(worker)
	}
	for reader := 0; reader < 4; reader++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for step := 0; step < 200; step++ {
				snapshot := manager.Snapshot()
				if snapshot.ChargeCurrentLimitMA < 0 || snapshot.DischargeCurrentLimitMA < 0 {
					t.Errorf("negative current limit: %+v", snapshot)
				}
			}
		}()
	}
	waitGroup.Wait()
}
