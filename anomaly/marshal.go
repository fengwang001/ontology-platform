package anomaly

import "encoding/json"

// MarshalJSON 把边掩码渲染成可读符号，例如 "WW"、"WR"、"RW"。
func (c Cycle) MarshalJSON() ([]byte, error) {
	names := make([]string, len(c.Edges))
	for i, e := range c.Edges {
		names[i] = edgeName(e)
	}
	return json.Marshal(struct {
		Txns  []int    `json:"txns"`
		Edges []string `json:"edges"`
	}{Txns: c.Txns, Edges: names})
}
