// Package ontology 实现本体链接图上按「深度 × 单节点扇出」双独立上限的
// 分页邻域遍历器，提供：
//
//   - 版本化（MVCC）的对象 / 链接 / 链接类型 / 权限存储；
//   - 基于 HMAC 签名、自描述的无状态续读标记，锚定标记产生时的快照版本；
//   - 三态截断标记（完整 / 因扇出截断 / 因深度截断），扇出优先于深度；
//   - 权限先于扇出计数的过滤规则（不可见对象不占扇出名额）；
//   - 每次请求的内部访问度量（ObjectsLoaded / LinksScanned / ACLChecks）。
//
// 典型用法：
//
//	s := ontology.NewStore()
//	acl := ontology.NewACL(s)
//	it := ontology.NewIterator(s, acl)
//	actor := ontology.Actor{ID: "alice"}
//
//	page, err := it.Traverse("object-1", 3, 20, "", actor)
//	for err == nil && !page.Done {
//	    page, err = it.Traverse("object-1", 3, 20, page.NextToken, actor)
//	}
package ontology
