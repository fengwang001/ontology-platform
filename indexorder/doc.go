// Package indexorder 实现索引序满足判定器：登记带方向与空值位置的
// 多列索引，并为给定的等值列集合与 ORDER BY 列表挑选能免去排序的
// 索引与扫描方向。
package indexorder
