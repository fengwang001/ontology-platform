# AUDIT — 不变量核对与复杂度实测

## 不变量逐条核对

1. **跨度精确**：`list/list.go` `Insert`（按 rank 差拆分前驱跨度、空指针跨度恒 0）
   与 `Delete`（前驱吸收被删节点跨度减 1、尾节点特判置 0）维护；`mset/mset.go`
   `Check` 逐层逐指针核验「跨度 == 实际跨过的底层元素数」、各层有序、底层总数 ==
   记录规模。测试：`mset.TestSpansExactAfterWrites`（500 插 250 删后 Check）、
   `mset.TestOrderIndependentStructure`（每种排列都 Check）。
2. **结构与插入顺序无关**：`key/key.go` `Level` 是键的纯函数（FNV-1a  trailing
   zeros），不读随机源与结构状态；等键副本塔形相同且相邻跨度恒 1。
   `mset.Mset.SameStructure` 逐字段比较（键、塔高、各层跨度、各层链接空/非空）。
   测试：`mset.TestOrderIndependentStructure`（两组多重集 × 全部旋转排列）。
3. **排名自洽**：`rank/rank.go` `At`（按跨度跳到第 k 个）与 `RankOf`（小于 k 的
   元素个数，缺失键即插入位置）互逆；`Range(lo,hi) = RankOf(hi)-RankOf(lo)`。
   测试：`rank.TestAtRankOfRange`（含缺失键、空区间）、`mset.TestRankConsistency`
   （逐元素互逆 + Range 与 RankOf 差值相等）。
4. **多重集语义**：`list.Insert` 允许重复（严格 `<` 定位，等键相邻）、`Delete`
   只删命中的第一个副本、`Count` 沿底层数等键；`RankOf` 因等键相邻而返回第一个
   的序号。测试：`mset.TestDuplicateSemantics`（RankOf/Count/Delete 三表自洽）。
5. **迭代器不撒谎**：选 fail-fast。`list` 版本号每次成功写 +1；`iter/iter.go`
   `Next` 先比对版本，不一致返回 `iter.ErrInvalidated`，绝不解引用已脱离结构的
   节点。测试：`rank.TestIterator`（全序遍历正确；插入、删除当前元素两种写均
   立即失效）。

## 访问节点数实测（rank 非导出计数器 `visited`）

计数器为 `rank.Rank` 的非导出字段（`atomic.Int64`，并发只读下无数据竞争），
不出现在 `mset` 公开接口；白盒测试 `rank.TestVisitedSublinear` 直接读取并断言。

| 元素数 | At(k) 访问节点 | RankOf(x) 访问节点 | 上限 |
|---|---|---|---|
| 1000 | 5 | 5 | 80 |
| 100000 | 10 | 10 | 80 |

元素数增长 100 倍，访问节点数仅从 5 涨到 10（约 2·log2(n) 量级），证明排名查询
沿跨度跳跃而非走底层链表。测试：`rank.TestVisitedSublinear` 断言两档均 <= 80。

## 故障注入与并发

- 四类可判定错误：`rank.ErrOutOfRange`（At 越界）、`rank.ErrBadRange`（lo>hi）、
  `list.ErrFull` / `list.ErrMaxLevel`（两类资源上限，超限即拒、无半插入节点）、
  `list.ErrNotFound`（删除不存在）。测试：`rank.TestAtRankOfRange`、
  `mset.TestErrorsAndLimits`（含「拒绝后仍可正常使用」与「失败操作未改变结构」）。
- 并发只读：`rank.TestConcurrentReads` 8 goroutine 同发 100 组只读查询（屏障起跑、
  无 sleep），结果逐位相同；`go test -race` 干净。
