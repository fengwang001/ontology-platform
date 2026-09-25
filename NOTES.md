# Trie 自动补全 + 引用计数删除：推导与不变量

## 第三节推导

插入 `apricot(5)`、`apple(5)`、`app(3)`、`application(2)` 后，`Complete("ap", 3)` 候选排序表：

| 串 | 频率 | 名次 | 入选 |
|---|---|---|---|
| apple | 5 | 1 | 是 |
| apricot | 5 | 2 | 是 |
| app | 3 | 3 | 是 |
| application | 2 | 4 | 否 |

`Delete("app")` 引用计数变化（只有 `"app"` 节点及其祖先受影响）：
root 4→3；`"a"` 4→3；`"ap"` 4→3；`"app"` 3→2（仍 >0，保留，仅摘掉终端标记）；`"appl"` 及以下、`"apr"` 及以下各节点不变；无任何剪枝。

- (甲) 同频错按插入先后：apricot 先于 apple 插入，top-3 错成 `[apricot, apple, app]`；正确是 `[apple, apricot, app]`。
- (乙) 忽略 k 会错返回 4 个（正确 3 个），多出的是 `application`，频率 2。
- (丙) `"app"` 终端节点同时是 `"apple"`、`"application"` 的前缀；若连同整棵子树剪掉会错删 `apple` 和 `application`。正确做法：引用计数从 3 减到 2，>0 故节点保留、只取消其终端标记；删后 `Complete("ap", 3)` = `[apple, apricot, application]`。

## 四条不变量及保证位置

1. 与朴素参照一致：`rank.TopK` 用大小为 k 的小顶堆做部分选择，等价于全排序取前 k；`api.verifyAll` 与朴素 map 参照逐前缀比对。测试：`TestTopKMatchesNaive`、`TestMatchesNaiveRandom`。
2. 前缀与排序正确：`trie.Walk` 只遍历 prefix 子树保证前缀性；`rank.better` 定义（频率降、字典序升）。测试：`TestTopKMatchesNaive`、`TestSection3`。
3. 引用计数自洽：`trie.Insert`/`trie.Delete` 沿路径维护 refs、归零即剪枝；`trie.CheckRefs` 朴素重数逐节点核验。测试：`TestRefsAndPrune`、`TestSelfCheck`。
4. 失败不留痕：`api` 层先校验（空串/非法 UTF-8/k≤0）再触碰 trie；`trie.Delete` 未命中时不写任何字段。测试：`TestErrorsDistinctAndStateless`。
