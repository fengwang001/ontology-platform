# NOTES

## 第三节推导

插入 apricot(5)、apple(5)、app(3)、application(2) 后，`Complete("ap",3)` 候选排序表：

| 串 | 频率 | 排序键(频降,字典升)名次 | 入选 top-3 |
|---|---|---|---|
| apple | 5 | 1（同频 'l'<'r'） | 是 |
| apricot | 5 | 2 | 是 |
| app | 3 | 3 | 是 |
| application | 2 | 4 | 否 |

`Delete("app")` 引用计数变化：root 4→3，a 4→3，ap 4→3，app 3→2（保留，仍是 apple/application 的祖先）；apr、appl 及其下游不变；app 节点仅清终端标记，不剪。

- (甲) 同频错按插入先后：top-3 错成 `[apricot, apple, app]`；正确 `[apple, apricot, app]`。
- (乙) 忽略 k 全返回：错返回 4 个，多出 `application`（频率 2）；正确 3 个。
- (丙) 整棵子树剪掉会错删 `apple`、`application`。正确：app 节点引用计数 3→2（>0，保留），仅去终端标记。删后 `Complete("ap",3)` = `[apple, apricot, application]`；错误实现则只剩 `[apricot]`。

## 四条不变量落点

1. 与朴素参照一致：`rank.TopK` 小顶堆部分选择 == 全排序取前 k；测试 `rank.TestTopKMatchesNaive`。
2. 前缀与排序正确：收集只走 `trie.Trie.collect` 前缀子树，堆比较器 (频降,字典升)；测试 `rank.TestTopKOrdering`、`trie.TestCollectOnlyPrefix`。
3. 引用计数自洽：`trie.Insert/Delete` 沿路径维护 refs，归零即剪；测试 `trie.TestRefcountInvariant`。
4. 失败不留痕：所有校验先于任何修改（`trie.CheckString`、`rank` 的 k 检查、Delete 先查存在性）；测试 `trie.TestRejectedOpsNoSideEffects`、`rank.TestTopKRejectsBadK`。
