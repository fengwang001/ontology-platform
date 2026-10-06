package bcontext

// desiredOpenerRefs 在开启时刻推导两个方向的组引用是否保留。
// 规则：任一侧文档自身声明同源策略且两侧跨源时，该侧方向断开；
// 开启者为同源策略时跨源弹窗两个方向均断开（组关系断开）；
// 同源允许弹窗的开启者始终保留自己指向被开启者的引用。
func desiredOpenerRefs(opener, openee Document) (openerToOpenee, openeeToOpener bool) {
	crossOrigin := opener.Origin != openee.Origin
	openerToOpenee = true
	openeeToOpener = true
	if !crossOrigin {
		return
	}
	if opener.Opener == OpenerSameOrigin {
		openerToOpenee = false
	}
	if opener.Opener == OpenerSameOrigin || openee.Opener == OpenerSameOrigin {
		openeeToOpener = false
	}
	return
}

// severEdge 按当前两份文档重新求值，并只做 true->false 的永久收紧；
// 断开不随后续导航恢复。
func (e *openerEdge) reevaluate() (changed bool) {
	fwd, back := desiredOpenerRefs(*e.opener.doc, *e.openee.doc)
	if !fwd && e.openerToOpenee {
		e.openerToOpenee = false
		changed = true
	}
	if !back && e.openeeToOpener {
		e.openeeToOpener = false
		changed = true
	}
	return
}

// markDetached 递归标记整棵子树失效：上下文仍在（id 可查），但文档不存在。
func markDetached(c *contextNode) {
	c.detached = true
	for _, child := range c.children {
		markDetached(child)
	}
}
