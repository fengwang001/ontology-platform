# 区间合并器推导笔记

## 相接区间的归属推导（需求第三节）

左闭右开语义下，`[1,2)` 含 1 不含 2，`[2,3)` 含 2，二者交集为空，
但并集 `[1,3)` 中间不留缝隙：任意 `1 <= x < 3` 都被覆盖。
若相接不合并，`Ranges()` 会把一段连续值域拆成两个区间，
同一属性出现"相接却分列"的矛盾表示（线上事故根因），
且合并结果随写入顺序变化，违反"顺序无关"。
因此推导出**相接即合并**：判定条件用 `a.end >= b.start`，
等号成立时两区间无缝相接，必须并入；若错用 `a.end > b.start`，
相接区间被拆成两段，违反需求第二节第 4 条（测试钉住，见下）。

## 语义条款落点（需求第二节）

- 1 左闭右开校验：`iv/iv.go` New/Validate；测试 TestValidate。
- 2 互不相交：`merge/merge.go` Add 的窗口合并；测试 TestMergedProperties。
- 3 覆盖不变：对照 `check/check.go` Ref.normalize；测试 TestMergedProperties。
- 4 相接即合并：`merge/merge.go` Add 中 End >= v.Start；测试 TestAbuttingMerge。
- 5 顺序无关：测试 TestMergedProperties 的随机置换。
- 第四节计数器：`merge/merge.go` total 字段；测试 TestMergedProperties（1e4 随机）。
- 第五节并发：`merge/merge.go` sync.Mutex；测试 TestConcurrentAdd。

## 哨兵错误

- `iv.ErrEmptyInterval`：`start >= end` 的基础错误（`errors.Is` 可判）。
- `iv.ErrInvertedInterval`：`start > end`，包装 `ErrEmptyInterval`。
- `merge.ErrInvalidInterval`：`Add` 收到非法区间，包装 iv 侧错误。
