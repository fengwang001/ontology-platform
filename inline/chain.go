package inline

// chain 是从根函数到当前调用点所在函数体的展开路径（含两端）。
// 链上只包含与当前展开路径相关的函数，因此基于链的任何判定
// 开销都与程序中无关函数的数量无关。
type chain []string

// top 返回直接包含当前调用点的函数（链尾）。
func (c chain) top() string { return c[len(c)-1] }

// count 统计函数 f 在当前展开链上出现的次数。
// 开销为 O(len(c))，只随链长增长，与无关函数数量无关。
func (c chain) count(f string) int {
	n := 0
	for _, g := range c {
		if g == f {
			n++
		}
	}
	return n
}

// extend 返回在链尾追加 f 后的新链，原链不被修改，
// 因此兄弟分支各自持有独立的链，计数互不影响。
func (c chain) extend(f string) chain {
	n := make(chain, len(c)+1)
	copy(n, c)
	n[len(c)] = f
	return n
}
