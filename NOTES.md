# BWT 推导与不变量

## 七行排序表（s="banana", term='$', t="banana$"）

| 行 | 旋转（字典序） | 末字符 |
|---|---|---|
| 0 | $banana | a |
| 1 | a$banan | n |
| 2 | ana$ban | n |
| 3 | anana$b | b |
| 4 | banana$ | $ |
| 5 | na$bana | a |
| 6 | nana$ba | a |

`last="annb$aa"`，`primary=4`（原串 "banana$" 在第 4 行）。LF 表为 `[1,5,6,4,0,2,3]`。

**(甲)** 从第 0 行出发走 LF 七步重构出 `$banana`（正确应为 `banana$`）。排序矩阵的第 k 行本身就是 t 的一个循环旋转，LF 游走重构的正是出发那一行；第 0 行是旋转 `$banana`，故得到的是 t 的旋转而非原串，只有从 primary 行出发才还原 t。

**(乙)** 报 `ErrTerminatorInInput`。终止符不唯一时串可能有周期（如 `a$a$` 旋转 2 位与自身相同），排序出现并列行、primary 不唯一；且 LF 锚定唯一循环的前提是 term 恰出现一次，多个 term 使逆映射分叉，无法确定哪次出现是串尾，逆变换不唯一。

**(丙)** `s=[]`：`last=['$']`，`primary=0`。`s="a"`：t="a$"，排序为 `["$a","a$"]`，`last=['a','$']`，`primary=1`。`Inverse` 得到 t 后要剥掉末尾一个 `term` 字节才还原 s。

## 四条不变量落点

1. 朴素参照一致：`rot.LastColumn` 实现即逐旋转排序取末列；`lf.Inverse` 即逐字符走 LF；`TestAgainstNaive`（api/api_test.go）用独立朴素实现双向比对。
2. 往返：`api.Transform`/`api.Inverse` 为纯函数；`TestRoundTrip` 钉住（含空串、单字符、重复字符、多档随机串）。
3. 末列是首列的排列：`rot.Sorted` 给出 F=sort(last)，计数表与 last 同源；`TestLastIsPermutation` 钉住。
4. 失败不留痕：`rot.Transform` 先校验 term 再计算，`lf.Inverse` 先校验 primary/term 计数再建表，出错返回 nil 且无可变状态；`TestFailureAtomicity` 钉住。
