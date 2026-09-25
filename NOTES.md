# NOTES

## 第三节推导（maxEntries=3）

| 步 | 操作 | 条目(值:计数) | MRU→LRU |
|---|---|---|---|
| 1 | Intern("a") | a:1 | a |
| 2 | Intern("b") | a:1 b:1 | b a |
| 3 | Intern("c") | a:1 b:1 c:1 | c b a |
| 4 | Intern("b") | a:1 b:2 c:1 | b c a |
| 5 | Release("c") | a:1 b:2 c:0 | b c a |
| 6 | Intern("d") 驱逐 c | a:1 b:2 d:1 | d b a |

(甲) 绝对 LRU 会错驱逐 `a`（计数 1，仍被引用）；正确驱逐 `c`——从 LRU 端起第一个计数==0 的条目。
(乙) 重复 Intern 不计数则 `b` 错成 1（正确 2）；之后一次 Release 就把它错降到 0，仍被引用的 `b` 会被驱逐，且与朴素重放计数错位。
(丙) 再 Release("c") 报「双重释放」，Release 未驻留串报「未驻留」，均为可判定哨兵错误；若减成 -1，计数与朴素重放永久错位，且负计数让"计数==0 才可驱逐"的判定失真，驱逐保护被破坏。

## 第二节不变量落实

1. 朴素参照一致：`pool.Pool.Intern/Release/evict`（pool.go）与 `api.naive` 同规则；`TestNaiveReplay`、`TestSelfCheck` 钉住。
2. 驻留唯一：`pool.items` 以值为键、每键对应唯一 `lru` 节点，`Len` 即键数；`TestNaiveReplay`、`TestSixStep` 钉住。
3. 驱逐保护：`pool.evict` 只从 LRU 端扫描并删除 `refs==0` 的条目，`refs>0` 永不删；`TestEvictionProtection` 钉住。
4. 失败不留痕：`api.New`/`pool.Intern`/`pool.Release` 全部先校验后改状态，拒绝则整体返回哨兵错误；`TestRejections` 钉住。
