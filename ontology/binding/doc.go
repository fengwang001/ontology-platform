// Package binding 实现链接类型跨对象类型字段绑定依据的兼容性校验。
//
// 核心概念：
//   - FieldDef：某版本下字段的不可变定义（类型、是否可缺失、枚举取值域）。
//   - BindingSpec：链接类型的绑定声明（两侧字段、方向、确定对应方式、缺失策略）。
//   - Check：无状态纯核验，依据两侧字段定义与声明返回四类结论之一。
//   - Registry：版本化字段管理、按链接类型隔离的核验状态、审计与并发安全查询。
//
// 结论优先级：field_deleted > incomparable_types / non_bijective / one_way_only。
// 单向绑定的反方向使用由 Query 返回 direction_denied，不静默退化为双向。
package binding
