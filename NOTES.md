# NOTES — Schema 演进下的 CDC 行解码

## 第三节推导：七行分步表（读 schema = v3，列序 id, full_name, email, age）

| 行 | 内容（`ID:名字=默认值` / 解码逐列来源） |
|---|---|
| v1 | `1:id=""` `2:name=""` `3:age=""` |
| v2 | `1:id=""` `2:full_name=""` `3:age=""` `4:email="none"` |
| v3 | `1:id=""` `2:full_name=""` `4:email="none"` `5:age="unknown"` |
| E1 v1 `["1","Ann","30"]` | `1`←第1位，`Ann`←第2位，`none`←默认值，`unknown`←默认值 → `["1","Ann","none","unknown"]` |
| E2 v2 `["2","Bob","41","b@x"]` | `2`←第1位，`Bob`←第2位，`b@x`←第4位，`unknown`←默认值 → `["2","Bob","b@x","unknown"]` |
| E3 v3 `["3","Cy","c@x","25"]` | `3`/`Cy`/`c@x`/`25`←第1–4位 → `["3","Cy","c@x","25"]` |
| E4 v2 `["4","Di","19",""]` | `4`←第1位，`Di`←第2位，`""`←第4位（空串原样），`unknown`←默认值 → `["4","Di","","unknown"]` |

- **(甲) 按位置映射**：E1→`["1","Ann","30","unknown"]`，email 错（`"30"`≠`"none"`）；E2→`["2","Bob","41","b@x"]`，email、age 两列错（应 `"b@x"`、`"unknown"`）。
- **(乙) 按列名映射**：E1→`["1","","none","30"]`，full_name 错（v1 无此名，取默认 `""`）、age 错（`"30"`≠`"unknown"`）；E2→`["2","Bob","b@x","41"]`，age 错；E4→`["4","Di","","19"]`，age 错。v3 的 `age` 是 Drop 后重新 Add 的**新列 ID=5**，E2 里的 `age` 是**旧列 ID=3**；ID 不同即不同列，ID 3 已删且永不复用，故 `"41"` 属于已删除的旧列，不能取。
- **(丙) 缺列填空串**：E1→`["1","Ann","",""]`，email、age 错（应 `"none"`/`"unknown"`）。空串当缺列填默认：E4→`["4","Di","none","unknown"]`，email 错（事件第4位的 `""` 是合法值，应原样保留）。

## 四条不变量：保证位置与钉住它的测试

1. **与逐版本迁移一致**：`decode.Decode` 按列 ID 经 `schema.Version.pos` 索引直接映射（`decode/decode.go`）；测试 `TestRandomReplayConsistency`（api/api_test.go，随机演进对照朴素重放）与 `api.SelfCheck`。
2. **ID 永不复用**：`schema.History.maxID` 单调递增，`Evolve` 只在全部 ops 试算成功后才提交（`schema/schema.go`）；测试 `TestIDNoReuse`。
3. **当前版本事件原样返回**：同版本各列 ID 必命中且位置相同，`Decode` 逐位取值含空串（`decode/decode.go`）；测试 `TestCurrentVersionPassthrough`。
4. **失败不留痕**：`Evolve` 全程在副本上试算、出错提前返回不碰 `maxID`；`Decode` 先校验后解码（`schema/schema.go`、`decode/decode.go`）；测试 `TestFailureAtomic`。
