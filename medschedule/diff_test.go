package medschedule

import "fmt"

// 随机差分测试：同一操作序列在高效实现与独立朴素模型上重放，
// 每一步比对错误码、计划点查询与 PRN 记录；步骤与判定依据写入日志文件。

type opKind int

const (
	opDrug opKind = iota
	opAllergy
	opCreate
	opStop
	opReplace
	opAdmin
	opRefuse
	opMakeup
	opPRN
	opQuery
)

type op struct {
	kind  opKind
	now   int64
	a, b  string
	n1    int64
	flag  bool
	spec  Spec
	oldID string
	lo    int64
	hi    int64
	who   string
}

func applyOne(sys *System, o op) (error, []Point, []PRNDose) {
	switch o.kind {
	case opDrug:
		return sys.RegisterDrug(o.now, o.a, o.b, o.n1), nil, nil
	case opAllergy:
		return sys.SetAllergy(o.now, o.who, o.a, o.flag), nil, nil
	case opCreate:
		return sys.CreateOrder(o.now, o.spec), nil, nil
	case opStop:
		return sys.StopOrder(o.now, o.a), nil, nil
	case opReplace:
		return sys.ReplaceOrder(o.now, o.oldID, o.spec), nil, nil
	case opAdmin:
		return sys.Administer(o.now, o.a), nil, nil
	case opRefuse:
		return sys.Refuse(o.now, o.a), nil, nil
	case opMakeup:
		return sys.MakeUp(o.now, o.a), nil, nil
	case opPRN:
		return sys.AdministerPRN(o.now, o.a), nil, nil
	case opQuery:
		pts, err := sys.QueryPatient(o.now, o.who, o.lo, o.hi)
		if err != nil {
			return err, nil, nil
		}
		prn, err := sys.QueryPRN(o.now, o.who)
		return err, pts, prn
	}
	return nil, nil, nil
}

func applyNaive(nv *NaiveModel, o op) (error, []Point, []PRNDose) {
	switch o.kind {
	case opDrug:
		return nv.RegisterDrug(o.now, o.a, o.b, o.n1), nil, nil
	case opAllergy:
		return nv.SetAllergy(o.now, o.who, o.a, o.flag), nil, nil
	case opCreate:
		return nv.CreateOrder(o.now, o.spec), nil, nil
	case opStop:
		return nv.StopOrder(o.now, o.a), nil, nil
	case opReplace:
		return nv.ReplaceOrder(o.now, o.oldID, o.spec), nil, nil
	case opAdmin:
		return nv.Administer(o.now, o.a), nil, nil
	case opRefuse:
		return nv.Refuse(o.now, o.a), nil, nil
	case opMakeup:
		return nv.MakeUp(o.now, o.a), nil, nil
	case opPRN:
		return nv.AdministerPRN(o.now, o.a), nil, nil
	case opQuery:
		return nil, nv.NaiveQuery(o.now, o.who, o.lo, o.hi), nv.NaivePRN(o.who)
	}
	return nil, nil, nil
}

func opName(k opKind) string {
	return []string{"RegisterDrug", "SetAllergy", "CreateOrder", "StopOrder", "ReplaceOrder",
		"Administer", "Refuse", "MakeUp", "AdministerPRN", "Query"}[k]
}

func describeOp(o op) string {
	switch o.kind {
	case opDrug:
		return fmt.Sprintf("RegisterDrug(now=%d name=%s cat=%s min=%d)", o.now, o.a, o.b, o.n1)
	case opAllergy:
		return fmt.Sprintf("SetAllergy(now=%d patient=%s item=%s active=%v)", o.now, o.who, o.a, o.flag)
	case opCreate:
		return fmt.Sprintf("CreateOrder(now=%d spec=%+v)", o.now, o.spec)
	case opStop:
		return fmt.Sprintf("StopOrder(now=%d id=%s)", o.now, o.a)
	case opReplace:
		return fmt.Sprintf("ReplaceOrder(now=%d old=%s spec=%+v)", o.now, o.oldID, o.spec)
	case opQuery:
		return fmt.Sprintf("Query(now=%d patient=%s [%d,%d])", o.now, o.who, o.lo, o.hi)
	default:
		return fmt.Sprintf("%s(now=%d id=%s)", opName(o.kind), o.now, o.a)
	}
}

func explainDecision(e error) string {
	if e == nil {
		return "accepted"
	}
	return "rejected: " + e.Error()
}
