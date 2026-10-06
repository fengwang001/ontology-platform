package traffic

const eps = 1e-9

// Config 为全局推演配置。
type Config struct {
	VehicleLength float64 // 每辆车占用的长度（与路段长度同单位）
}

// Link 描述一条有向路段。
type Link struct {
	ID       int
	From, To int // 节点
	Length   float64
	Capacity float64 // 单位时间可通过的车辆数
	Arrival  float64 // 恒定到达流量
}

// Network 为静态路网拓扑。
type Network struct {
	cfg      Config
	links    map[int]*Link
	upstream map[int][]int // linkID -> 直接上游路段 ID
	outgoing map[int][]int // nodeID -> 从该节点出发的路段 ID
	incoming map[int][]int // nodeID -> 汇入该节点的路段 ID
}

// BuildNetwork 校验并构建路网。
func BuildNetwork(links []Link, cfg Config) (*Network, error) {
	if !(cfg.VehicleLength > 0) {
		return nil, ErrInvalidArgument
	}
	if len(links) == 0 {
		return nil, ErrInvalidArgument
	}

	byID := make(map[int]*Link, len(links))
	outgoing := make(map[int][]int)
	incoming := make(map[int][]int)
	for i := range links {
		l := links[i] // 每次迭代的独立变量，供取址
		if _, dup := byID[l.ID]; dup {
			return nil, ErrInvalidArgument
		}
		if !(l.Length > 0) || !(l.Capacity > 0) || l.Arrival < 0 {
			return nil, ErrInvalidArgument
		}
		if l.Arrival > l.Capacity+eps {
			return nil, ErrFlowExceedsCapacity
		}
		link := l
		byID[l.ID] = &link
	}

	upstream := make(map[int][]int, len(links))
	for i := range links {
		l := links[i]
		outgoing[l.From] = append(outgoing[l.From], l.ID)
		incoming[l.To] = append(incoming[l.To], l.ID)
	}
	for i := range links {
		l := links[i]
		up := incoming[l.From] // 直接上游 = 流入本路段起点节点的边
		cp := make([]int, len(up))
		copy(cp, up)
		upstream[l.ID] = cp
	}

	return &Network{links: byID, upstream: upstream, cfg: cfg,
		outgoing: outgoing, incoming: incoming}, nil
}

func (n *Network) link(id int) (*Link, bool) {
	l, ok := n.links[id]
	return l, ok
}

func (n *Network) upstreamOf(id int) []int { return n.upstream[id] }

func (n *Network) capacityInVehicles(l *Link) float64 {
	return l.Length / n.cfg.VehicleLength
}
