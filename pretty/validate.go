package pretty

// 本文件实现渲染前的只读分析通道：在一次迭代式后序遍历中完成
//   - 参数校验（文本含换行字符、缩进增量为负）
//   - 嵌套深度计算（含引用展开）
//   - 未登记引用收集
//   - 每个节点的平铺/断开扫描剖面（供放得下判定 O(1) 查询）
//
// 错误报告优先级：参数非法 > 文档过深 > 未登记引用。

// profile 描述"从当前列开始扫描某节点（在某模式下）直到第一个断开的
// 可断点或强制换行（或子树结束）"的累计结果。
type profile struct {
	w    int  // 累计宽度（饱和于 satCap）
	stop bool // 是否在中途遇到断开的可断点或强制换行而停止
}

// nodeInfo 是每个节点的标注。
type nodeInfo struct {
	flat  profile // 按平铺模式扫描该节点的剖面
	brk   profile // 按断开模式扫描该节点的剖面
	depth int     // 子树嵌套深度（含引用展开）
}

// satCap 是宽度饱和上限，避免病态输入下溢出。
const satCap = int64(1) << 60

func satAdd(a, b int) int {
	sum := a + b
	if sum < 0 || sum >= int(satCap) {
		return int(satCap)
	}
	return sum
}

// checkParams 对单个片段自身（不展开引用）做参数校验，返回第一个
// 非法参数的描述与引用到的片段名清单。
func checkParams(root *node) (msg string, refs []string) {
	stack := []*node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch n.kind {
		case kindText:
			if hasNewline(n.text) {
				return "文本含换行字符", nil
			}
		case kindCond:
			if hasNewline(n.text) || hasNewline(n.alt) {
				return "条件文本含换行字符", nil
			}
		case kindIndent:
			if n.n < 0 {
				return "缩进增量为负", nil
			}
			stack = append(stack, n.child)
		case kindAlign, kindGroup:
			stack = append(stack, n.child)
		case kindSeq:
			stack = append(stack, n.children...)
		case kindRef:
			refs = append(refs, n.name)
		}
	}
	return "", refs
}

func hasNewline(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return true
		}
	}
	return false
}

// analyze 对文档树（含引用展开）做只读分析。frags 为会话快照。
// 共享节点（同一子树被多处使用、片段被多处引用）只标注一次。
func analyze(root *node, frags map[string]Doc) (map[*node]nodeInfo, *Error) {
	info := make(map[*node]nodeInfo)
	var invalidMsg string
	unregistered := map[string]bool{}

	type frame struct {
		n       *node
		visited bool
	}
	stack := []frame{{n: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := f.n
		if _, ok := info[n]; ok {
			continue
		}
		if !f.visited {
			stack = append(stack, frame{n: n, visited: true})
			switch n.kind {
			case kindIndent, kindAlign, kindGroup:
				stack = append(stack, frame{n: n.child})
			case kindSeq:
				for _, c := range n.children {
					stack = append(stack, frame{n: c})
				}
			case kindRef:
				if frag, ok := frags[n.name]; ok {
					stack = append(stack, frame{n: asNode(frag)})
				}
			}
			continue
		}
		info[n] = summarize(n, frags, info, &invalidMsg, unregistered)
	}

	if invalidMsg != "" {
		return nil, &Error{Code: ErrInvalidParam, Detail: invalidMsg}
	}
	if info[root].depth > MaxDepth {
		return nil, &Error{Code: ErrTooDeep, Detail: "嵌套深度超过 1000"}
	}
	for name := range unregistered {
		return nil, &Error{Code: ErrUnregisteredRef, Detail: name}
	}
	return info, nil
}

// summarize 在所有子节点都已有标注后，计算单个节点的标注。
func summarize(n *node, frags map[string]Doc, info map[*node]nodeInfo, invalidMsg *string, unregistered map[string]bool) nodeInfo {
	switch n.kind {
	case kindText:
		if hasNewline(n.text) && *invalidMsg == "" {
			*invalidMsg = "文本含换行字符"
		}
		p := profile{w: Width(n.text)}
		return nodeInfo{flat: p, brk: p, depth: 1}
	case kindCond:
		if (hasNewline(n.text) || hasNewline(n.alt)) && *invalidMsg == "" {
			*invalidMsg = "条件文本含换行字符"
		}
		return nodeInfo{
			flat:  profile{w: Width(n.text)},
			brk:   profile{w: Width(n.alt)},
			depth: 1,
		}
	case kindSpace:
		return nodeInfo{
			flat:  profile{w: 1},
			brk:   profile{stop: true},
			depth: 1,
		}
	case kindBlank:
		return nodeInfo{
			flat:  profile{},
			brk:   profile{stop: true},
			depth: 1,
		}
	case kindHardLine:
		p := profile{stop: true}
		return nodeInfo{flat: p, brk: p, depth: 1}
	case kindIndent:
		if n.n < 0 && *invalidMsg == "" {
			*invalidMsg = "缩进增量为负"
		}
		c := info[n.child]
		return nodeInfo{flat: c.flat, brk: c.brk, depth: c.depth + 1}
	case kindAlign:
		c := info[n.child]
		return nodeInfo{flat: c.flat, brk: c.brk, depth: c.depth + 1}
	case kindGroup:
		c := info[n.child]
		// 断开模式下扫描一个组等价于平铺扫描其内容（组在放得下判定中
		// 总是按平铺计入）。
		return nodeInfo{flat: c.flat, brk: c.flat, depth: c.depth + 1}
	case kindSeq:
		var flat, brk profile
		depth := 1
		for _, k := range n.children {
			ki := info[k]
			if ki.depth+1 > depth {
				depth = ki.depth + 1
			}
			if !flat.stop {
				flat.w = satAdd(flat.w, ki.flat.w)
				flat.stop = ki.flat.stop
			}
			if !brk.stop {
				brk.w = satAdd(brk.w, ki.brk.w)
				brk.stop = ki.brk.stop
			}
		}
		return nodeInfo{flat: flat, brk: brk, depth: depth}
	case kindRef:
		frag, ok := frags[n.name]
		if !ok {
			unregistered[n.name] = true
			return nodeInfo{depth: 1}
		}
		// 引用就地展开：引用节点本身不增加深度。
		fi := info[asNode(frag)]
		return nodeInfo{flat: fi.flat, brk: fi.brk, depth: fi.depth}
	}
	return nodeInfo{depth: 1}
}
