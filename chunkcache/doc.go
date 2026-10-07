// Package chunkcache 实现内容分发边缘节点的大对象分片缓存。
//
// 对象按固定大小 S 切片，最后一片可不足 S；缓存最多驻留 C 个切片。
// 字节范围请求在切片粒度做部分命中，仅对缺失的连续切片段回源。
//
// 核心抽象：
//   - [Cache]：并发安全的分片缓存，入口为 [Cache.Get]。
//   - [Origin]：调用方注入的源站，按 (Key, First, Last, ExpectedVersion) 取片。
//   - [ByteRange]：三种范围写法（起止 / 起点至末尾 / 末尾 N 字节）。
//   - [Error]：带固定优先级的结构化错误，可用 errors.Is 匹配哨兵。
//
// 详见仓库根目录 DESIGN.md 的设计说明与取舍。
package chunkcache
