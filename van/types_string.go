package van

import "fmt"

func (k Category) String() string {
	switch k {
	case General:
		return "普通"
	case Food:
		return "食品"
	case Flammable:
		return "易燃"
	case Oxidizer:
		return "氧化"
	default:
		return fmt.Sprintf("类别(%d)", int(k))
	}
}

func (r RejectReason) String() string {
	switch r {
	case ReasonOrder:
		return "顺序冲突"
	case ReasonIsolation:
		return "隔离冲突"
	case ReasonOverWeight:
		return "超重"
	case ReasonOverVolume:
		return "超容"
	default:
		return "未知原因"
	}
}

func (c Cargo) String() string {
	return fmt.Sprintf("货物#%d{重%d克 容%dcm³ 停靠点%d %s}", c.ID, c.Weight, c.Volume, c.Stop, c.Kind)
}
