// Package bitemporal 判定本体实例快照在不同格式版本之间迁移时，
// 有效时间轴与事务时间轴信息能否被保留。
//
// 组件由三个职责清晰、相互协作的部分构成：
//   - 记录自身时态区间的自洽性校验（interval.go / record.go）
//   - 边界语义比较（boundary.go）
//   - 版本间信息留存规则判定（version.go / judge.go / migrate.go）
package bitemporal
