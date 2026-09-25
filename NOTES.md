# NOTES

## 八行分步表（schema 有序：id:Int 必需， name:Str 必需， score:Int 可选=0, active:Bool 可选=false；* = 默认填充）

| # | 事件 | 判定 | 拒绝类别 | 规范化记录 |
|---|------|------|----------|------------|
| 1 | {id:1, name:"a"} | 接受 | - | {id:1, name:"a", score:0*, active:false*} |
| 2 | {id:2, name:"b", score:5} | 接受 | - | {id:2, name:"b", score:5, active:false*} |
| 3 | {id:3} | 拒绝 | 缺必需字段 name | 无 |
| 4 | {id:4, name:"c", active:true} | 接受 | - | {id:4, name:"c", score:0*, active:true} |
| 5 | {id:5, name:"d", extra:123} | 接受 | - | extra 静默丢弃；{id:5, name:"d", score:0*, active:false*} |
| 6 | {id:6, name:7} | 拒绝 | 类型不匹配 name(Int≠Str) | 无 |
| 7 | {id:7, name:"f", score:9, active:false} | 接受 | - | {id:7, name:"f", score:9, active:false} |
| 8 | {id:8, name:"g", active:"yes"} | 拒绝 | 类型不匹配 active(Str≠Bool) | 无 |

- (甲) `extra` 静默丢弃，不报错、不影响任何 schema 字段。若误把未知字段当致命错误，会错报「未知字段」拒绝；本应接受并产出 {id:5, name:"d", score:0, active:false}。
- (乙) 应拒绝（缺必需 name）。若错误地用零值填充，会错误接受并产出 {id:3, name:"", score:0, active:false}。
- (丙) 应拒绝（name 类型 Int≠Str，不做转换）。若宽松转换 Int→Str，会错误接受并产出 {id:6, name:"7", score:0, active:false}。

## 四条不变量：保证位置与钉住它的测试

1. 与朴素参照一致：`schema.Schema.Validate` 先哈希索引逐事件字段类型检查，再按 schema 顺序解出首个错误（schema.go）；测试 `TestValidateMatchesNaive`、`TestEightEventSequence`。
2. 兼容降级不丢数据：输出循环按 schema 顺序填满全部字段（原值或 Default），未知字段仅 `continue` 丢弃（schema.go）；测试 `TestEightEventSequence`。
3. 拒绝确定性：两种拒绝各返回互不相同哨兵错误且记录为 nil（field.go 哨兵、schema.go 错误分支）；测试 `TestRejectDistinct`、`TestEightEventSequence`。
4. 失败不留痕：错误路径只动局部变量，计数器仅成功时 `Store`，`New` 失败返回 nil（schema.go）；测试 `TestRejectLeavesNoTrace`、`TestInvalidSchemaRejected`、`TestCheckCountIndependentOfSchemaSize`。
