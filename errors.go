package ontology

import "errors"

var (
	// ErrInvalidArgument：构造或调用参数非法（含批内任一事件非法）。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrCapacity：追加后事件总数将超过 Emax。
	ErrCapacity = errors.New("capacity exceeded")
	// ErrUnknownRole：角色未在角色表中声明。
	ErrUnknownRole = errors.New("unknown role")
	// ErrForbidden：某维度敏感级别高于角色可见级别。
	ErrForbidden = errors.New("forbidden: insufficient clearance")
	// ErrTooLarge：行集×列集超过 Cmax。
	ErrTooLarge = errors.New("result too large")
)
