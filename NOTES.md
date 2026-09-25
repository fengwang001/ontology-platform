# NOTES — Harris 无锁有序链表

## 第三节：八步分步表（链表内容 = 未删除 key 升序）

| 步 | 操作 | 操作后链表 | 返回值/错误 |
|---|---|---|---|
| 1 | Insert(5) | [5] | nil |
| 2 | Insert(2) | [2 5] | nil |
| 3 | Insert(8) | [2 5 8] | nil |
| 4 | Insert(5) | [2 5 8] | ErrDuplicate（状态不变） |
| 5 | Contains(2) | [2 5 8] | true |
| 6 | Delete(2) | [5 8] | nil（先 mark 节点2，再摘除） |
| 7 | Contains(2) | [5 8] | false |
| 8 | Insert(3) | [3 5 8] | nil |

- **(甲)** 第 4 步报「重复」错误（ErrDuplicate）。若不做重复检测直接插入，链表变成 [2 5 5 8]，出现重复 key，违反不变量 2「升序且无重复」，也连带破坏不变量 1（朴素参照的 List 无重复，两边不再一致）。
- **(乙)** 若改成不 mark、直接一步摘除：在节点 2 尚未被摘除的瞬间，并发 `Contains(2)` 沿链走到节点 2，见其未被标记，返回 **true**——这是陈旧读，Delete 已生效（或正在生效）却读到旧值。先 mark 的实现此刻 `Contains(2)` 立即返回 false：mark 是删除的"生效点"，摘除只是清理，二者差在删除语义的原子可见性。
- **(丙)** 节点 2 的 next = 「指向节点 5 的指针 | 1」。不清 mark 位直接解引用：地址被偏移了 1 字节，是野指针，读到未对齐/非法内存——轻则读到垃圾数据（key 是随机值，遍历结果错乱），重则 panic/段错误；`Contains(5)` 无法保证返回 true，大概率返回错误结果或崩溃。正确实现先 `&^ 1` 清位再解引用，走到节点 5，`Contains(5)` 返回 true。

## 第二节：四条不变量的保证位置与钉住测试

1. **与朴素参照一致**：`hlist` 的 find/Insert/Delete 严格按升序定位、CAS 失败重试；测试 `TestMatchesNaiveReference`（随机操作序列对拍 mutex 保护的参照实现）。
2. **升序且无重复**：`hlist.List.Insert` 只在 `curr.Key > k` 处插入、`curr.Key == k` 且未 mark 时报重复；测试 `TestSortedUnique`。
3. **删除可见性**：`hlist.List.Delete` 先 CAS 置 mark 位（`lnode.Node.MarkNext`），`Contains` 跳过 mark 节点；测试 `TestDeleteVisibility`。
4. **失败不留痕**：三类拒绝路径都在任何 CAS 之前返回哨兵错误；测试 `TestRejectedOpsLeaveNoTrace`。

另：物理摘除 O(1) 由非导出计数器 `unlinkVisited` 记录、同包测试 `TestUnlinkIsConstant` 直接读该字段钉住（不经任何导出接口）；并发正确性由 `TestConcurrent`（-race）钉住。
