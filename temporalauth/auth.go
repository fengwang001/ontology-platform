package temporalauth

// Viewer 是查询者身份信息。
type Viewer struct {
	ID     string
	ZoneID string // 查询者所在时区（仅用于解读查询者本地输入，永不作为归一化基准）
}

// ViewRequest 是一次属性查看请求。
type ViewRequest struct {
	RequestID  string
	ObjectID   string
	ObjectType string
	Property   string
	Viewer     *Viewer
	Now        Instant // 判定时刻（UTC 绝对时刻）
}

// Decision 是对外的判定结果。拒绝/错误时只携带稳定代码，不携带任何取值、
// 时区或窗口边界信息；仅在允许时返回记录。
type Decision struct {
	Allowed bool
	Code    ErrorCode
	Value   *TemporalRecord
}

// Service 是属性级权限校验服务。
type Service struct {
	store      *Store
	audit      *AuditLog
	naive      *NaiveDecider
	auditOn    bool
	crossCheck bool
}

// NewService 构造服务。
func NewService(store *Store, audit *AuditLog) *Service {
	return &Service{
		store:      store,
		audit:      audit,
		naive:      NewNaiveDecider(),
		auditOn:    true,
		crossCheck: true,
	}
}

// SetCrossCheck 开关生产路径与朴素模型的逐条对照（默认开启）。
func (s *Service) SetCrossCheck(on bool) { s.crossCheck = on }

// SetAudit 开关服务端审计记录（默认开启）。
func (s *Service) SetAudit(on bool) { s.auditOn = on }

// View 执行归一化与权限判定。
//
// 基准选取：一律以“对象所属地区在判定时刻生效的默认时区版本”对窗口边界
// 归一化；记录取值的归一化基准在录入时即按“录入时刻该地区生效版本”冻结。
// 查询者时区与录入标注时区从不作为基准，选取规则对所有请求一致。
//
// 错误优先级见 errors.go：地区时区不可确定 > 窗口规则无效 > 查询者缺失 >
// 属性废弃；多类错误同时成立时只报告最高优先级一类。
func (s *Service) View(req ViewRequest) Decision {
	probesBefore := s.store.RegionProbes()
	snap := s.store.Snapshot()

	entry := AuditEntry{
		RequestID:  req.RequestID,
		Seq:        snap.seq,
		ObjectID:   req.ObjectID,
		ObjectType: req.ObjectType,
		Property:   req.Property,
		Now:        req.Now,
		Outcome:    "error",
	}
	if req.Viewer != nil {
		entry.ViewerID = req.Viewer.ID
	}

	decision := s.decide(snap, req, &entry)
	entry.ProbeCount = int(s.store.RegionProbes() - probesBefore)

	if s.crossCheck {
		naive := s.naive.Decide(snap, req)
		entry.CrossCheck = true
		entry.CrossMatch = (naive.Allowed == decision.Allowed) && (naive.Code == decision.Code)
	}

	if s.auditOn && s.audit != nil {
		s.audit.Append(entry)
	}
	return decision
}

func (s *Service) decide(snap Snapshot, req ViewRequest, entry *AuditEntry) Decision {
	// 1. 归一化基准：对象所属地区当前生效默认时区。
	regionID := snap.objectRegion[req.ObjectID]
	region, ok := snap.regions[regionID]
	if !ok {
		return s.fail(entry, ErrRegionTimezoneUnresolved)
	}
	regIdx, ok := snap.resolveRegionIndex(&region, req.Now)
	if !ok {
		return s.fail(entry, ErrRegionTimezoneUnresolved)
	}
	zone, ok := snap.catalog.Get(region.Versions[regIdx].ZoneID)
	if !ok {
		return s.fail(entry, ErrRegionTimezoneUnresolved)
	}
	entry.BasisRegionID = regionID
	entry.BasisVersion = regIdx
	entry.BasisZoneID = zone.ID

	objectTypeID := snap.objects[req.ObjectID]

	// 2. 窗口规则。
	byProp := snap.windows[objectTypeID]
	var chain WindowChain
	hasChain := false
	if byProp != nil {
		chain, hasChain = byProp[req.Property]
	}
	if !hasChain {
		return s.fail(entry, ErrWindowRuleInvalid)
	}
	rule, _, ok := chain.RuleAt(req.Now)
	if !ok {
		return s.fail(entry, ErrWindowRuleInvalid)
	}
	if err := rule.Validate(); err != nil {
		return s.fail(entry, ErrWindowRuleInvalid)
	}
	start, end := ResolveBounds(rule, zone)
	entry.WindowStart = start
	entry.WindowEnd = end

	// 3. 查询者身份（仅当需要其参与时检查；缺失时报统一错误）。
	if req.Viewer == nil || req.Viewer.ID == "" {
		return s.fail(entry, ErrViewerMissing)
	}

	// 4. 属性是否在当前对象类型版本被废弃。
	ot := snap.objectTypes[objectTypeID]
	spec, found := ot.PropertyAt(req.Property, req.Now)
	if !found || spec.Deprecated {
		return s.fail(entry, ErrPropertyDeprecated)
	}

	// 5. 记录存在性与窗口判定（半开 [start,end)）。
	recMap := snap.records[req.ObjectID]
	var rec TemporalRecord
	foundRec := false
	if recMap != nil {
		rec, foundRec = recMap[req.Property]
	}
	if !foundRec {
		return s.fail(entry, ErrDenied)
	}
	entry.NormalizedAt = rec.normalizedAt
	if !WithinWindow(req.Now, start, end) {
		return s.fail(entry, ErrDenied)
	}

	entry.Outcome = "allow"
	entry.ErrorCode = ""
	value := rec
	return Decision{Allowed: true, Value: &value}
}

func (s *Service) fail(entry *AuditEntry, code ErrorCode) Decision {
	if code == ErrDenied {
		entry.Outcome = "deny"
	} else {
		entry.Outcome = "error"
	}
	entry.ErrorCode = code
	return Decision{Allowed: false, Code: code}
}
