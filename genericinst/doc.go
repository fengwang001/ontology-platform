// Package genericinst 提供编译器后端的泛型实例化登记、去重、依赖跟踪与配额控制。
//
// 典型用法：
//
//	r := genericinst.New(
//	    genericinst.WithMaxInstances(10000),
//	    genericinst.WithMaxPerDef(256),
//	    genericinst.WithMaxDepth(32),
//	    genericinst.WithLogger(genericinst.NewTextLogger(os.Stderr)),
//	)
//	r.RegisterDef(&genericinst.Def{
//	    Name:   "List",
//	    Params: []genericinst.Param{{Name: "T"}},
//	})
//
//	in, err := r.Request("compilation-unit-a", "List",
//	    []genericinst.Type{genericinst.NewNominal("int")},
//	    func(s *genericinst.Session) error {
//	        // 实例化体内可嵌套请求别的定义，依赖边自动登记。
//	        _, e := s.Request("Eq", []genericinst.Type{genericinst.NewNominal("int")}, nil)
//	        return e
//	    })
//
// 语义要点：
//   - 类型实参经规范化后判等：别名等价于目标；结构类型字段名、次序、类型全一致才等价；
//     省略带默认值的形参按默认值补齐；省略无默认值形参报参数错误。
//   - 等价实参列表（任意编译单元、任意写法）命中同一实例；命中不消耗配额且为 O(1)。
//   - 嵌套实例化共享同一串行临界区；超深度/配额/嵌套错误整体撤销本次新建实例，
//     已存在并被复用的实例不受影响。
//   - 更新定义立即把其全部实例及传递依赖标记过期；过期实例不可命中但可按 ID 查询
//     过期原因与传导层数；清理仅在「已过期且无未过期依赖者」时成功，否则返回依赖错误。
//   - 拒绝次序固定：未定义 > 参数 > 约束 > 深度 > 配额。
//   - 所有方法可被多编译单元并发调用，结果等价于某种全局串行顺序；
//     SnapshotView 返回同一瞬间的有效/过期数、分定义计数、累计命中与新建数。
package genericinst
