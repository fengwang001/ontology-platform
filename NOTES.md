# NOTES

记法：A=Added R=Removed C=Changed，形如 `C{a,1,2}` 即 Changed{Col:a,Old:1,New:2}；`c:` 表示 `c:""`。列按字典序。

| 步 | Before | after | 本步变更 | 步后当前行 |
|---|---|---|---|---|
| 1 | {} | {a:1,b:x} | A{a,,1} A{b,,x} | {a:1,b:x} |
| 2 | {a:1,b:x} | {a:1,b:y,c:} | C{b,x,y} A{c,,} | {a:1,b:y,c:} |
| 3 | {a:1,b:y,c:} | {a:2,b:y,c:} | C{a,1,2} | {a:2,b:y,c:} |
| 4 | {a:2,b:y,c:} | {a:2,c:} | R{b,y,} | {a:2,c:} |
| 5 | {a:2,c:} | {c:z} | R{a,2,} C{c,,z} | {c:z} |
| 6 | {c:z} | {} | R{c,z,} | {} |

- (甲) 正确仅 C{a,1,2}（b、c 相等跳过）。相等也报 Changed 的错误实现会多产 C{b,y,y} 与 C{c,,}。
- (乙) 正确产 A{c,,}：空串是真实列值，c 存在。误把空串当不存在会漏掉 A{c,,}，当前行 c 被记成不存在；到第 5 步 c 错成 A{c,,z}（正确为 C{c,,z}）。
- (丙) 正确产 R{b,y,}。只遍历 after 列的错误实现第 4 步什么都不产，下游视图 b 残留旧值 "y"。

## 四条不变量

1. 重算一致：row/row.go Apply 先追加 diff 再整行替换、Recompute 对空行→当前行重算；测试 TestReplayMatchesView 钉住。
2. 只标变化列：diff/diff.go 归并循环仅对共有列计数比较一次且不等才产出，Old/New 取真实值；测试 TestOnlyChangedColumns 钉住。
3. 顺序确定+空串区分：diff/diff.go 对两行列名并集排序后双指针归并，存在性只看 map 成员；测试 TestOrderingAndEmptyString 钉住。
4. 失败不留痕：api/api.go 在改状态前完成全部校验（nil/空 Key/空列名哨兵错误）；测试 TestRejectedEventsLeaveNoTrace 钉住。
