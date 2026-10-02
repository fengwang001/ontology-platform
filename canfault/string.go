package canfault

func (e Event) String() string {
	switch e {
	case TxOK:
		return "TxOK"
	case TxErr:
		return "TxErr"
	case TxAckErr:
		return "TxAckErr"
	case RxOK:
		return "RxOK"
	case RxErr:
		return "RxErr"
	case RxErrDominant:
		return "RxErrDominant"
	default:
		return "InvalidEvent"
	}
}

func (s State) String() string {
	switch s {
	case ErrorActive:
		return "ErrorActive"
	case ErrorPassive:
		return "ErrorPassive"
	case BusOff:
		return "BusOff"
	default:
		return "UnknownState"
	}
}
