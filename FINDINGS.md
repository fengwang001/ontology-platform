# FINDINGS: 快照格式实现行为与文档承诺的不一致

测试依据：`characterization_test.go`（全部断言当前真实行为，均通过）。
涉及源码：`read.go`（`scanFrame`、`Read`）、`write.go`（`Write`）、
`format.go`（`maxBodySize`）、`errors.go`（哨兵文档）、`inspect.go`。

## Finding 1: 长度前缀分类靠固定 64MB 阈值，而非「是否塞得进余量」

- 文档承诺：`errors.go` 中 `ErrLengthTooLarge` 注释为 "a length prefix
  exceeds the remaining file"，即「超出剩余字节」就应归此类。
- 复现输入：对同一小文件（记录区约 100 字节）篡改首条记录的长度前缀：
  (a) 填 `0x00010000`（64KB，远超余量但远小于 64MB）；
  (b) 填 `0xFFFFFFFF`。
- 实际输出：
  - (a) `Read` 报 `ErrRecordTruncated`（`errors.Is` 命中），与「文件在
    记录中途被截断」完全同类；`Inspect` 给 `BadErr=ErrRecordTruncated`、
    `BadIndex=0`、`Skipped=2`、`RegionErr=true`、`CountErr=nil`。
  - (b) `Read` 报 `ErrLengthTooLarge`；`Inspect` 对应
    `BadErr=ErrLengthTooLarge`，其余同上。
  - 分界点实测为 `claimed > 64MB`（严格大于）：claim 恰为 64MB 仍报
    `ErrRecordTruncated`，64MB+1 才报 `ErrLengthTooLarge`。
- 应当输出：按 `ErrLengthTooLarge` 的文档语义，(a)(b) 同属「长度前缀
  虚报、超出文件余量」，应都归为 `ErrLengthTooLarge`（或文档应改述为
  「超过 64MB 固定上限」）。现状下用 `errors.Is(err, ErrLengthTooLarge)`
  探测「损坏的长度前缀」会漏掉所有不超过 64MB 的虚报。
- 根因：`read.go` `scanFrame` 中，当 `total > len(body)-pos` 时用
  `claimed > maxBodySize`（`format.go` 的 `64 << 20`）这个固定绝对阈值
  分流，而不是用「claim 是否超出剩余字节」这一相对条件。注释只说
  "implausibly large"，未说明 64MB 阈值与「塞不进余量」的语义差异。

## Finding 2: `Read` 不校验「primary-key ordered」

- 文档承诺：`errors.go` 包注释称文件含 "primary-key ordered
  records"；`write.go` 的 `Write` 注释称记录按键排序、乱序输入产出
  相同字节。
- 复现输入：用真实帧格式手工构造一份文件（CRC 逐记录全对、count 与
  region CRC 全对），记录按键序 `bravo, alpha, charlie` 存放（乱序）。
- 实际输出：`Read` 不报错，按文件字节顺序原样返回
  `[bravo, alpha, charlie]`；`Inspect` 报告完全干净（`BadIndex=-1`、
  `BadErr=nil`、`Skipped=0`、`CountErr=nil`、`RegionErr=false`）。
- 应当输出：若「有序」是格式不变量，`Read` 应拒绝乱序文件（或至少
  `Inspect` 应标注）；否则包注释应明确「有序仅是写侧约定，读侧不校验」。
- 根因：`Read`（`read.go`）只按字节顺序逐帧 `scanFrame` 解码，从未
  比较相邻记录的键；排序保证只存在于 `Write` 单侧。CRC 只保护字节
  完整性，不保护键序语义，伪造文件可绕过该约定而不被任何一级 CRC
  发现。

## Finding 3: 主键唯一性两侧都不保证

- 文档承诺：`format.go` 称 `Record` 是 "keyed by a primary key
  string"，暗示主键唯一。
- 复现输入：构造同主键两条记录的文件（`alpha/1/first`、
  `alpha/2/second`、`bravo/3/b`，校验和全对）；另验证 `Write` 直接
  接受含重复键的输入切片。
- 实际输出：
  - `Read` 不报错，原样返回全部 3 条（两条 `alpha` 都在）；
  - `Inspect` 报告完全干净，同样返回 3 条；
  - `Write` 不查重，稳定排序后把两条重复键记录都写入。
- 应当输出：若主键唯一是格式承诺，`Write` 应拒绝重复键输入、`Read`
  应拒绝含重复键的文件；否则文档应声明键可重复。现状下下游用返回
  切片建 `map[key]record` 会静默覆盖先出现的记录，造成数据丢失。
- 根因：`Write` 只做 `sort.SliceStable`（重复键相邻保留），无唯一性
  检查；`Read` 逐帧解码后仅追加到切片，不维护已见键集合。唯一性承诺
  只存在于文档措辞中，没有任何代码路径执行它。

## 汇总

| # | 承诺来源 | 实现位置 | 分歧 |
|---|----------|----------|------|
| 1 | `ErrLengthTooLarge` 注释（errors.go） | `scanFrame`（read.go） | 固定 64MB 阈值 vs 「超出余量」 |
| 2 | 包注释 "primary-key ordered"（errors.go） | `Read`（read.go） | 写侧排序，读侧不校验 |
| 3 | "primary key" 措辞（format.go） | `Write`/`Read` | 两侧均不保证唯一 |

以上均为「测试钉住现状」，未改动任何实现文件；是否修实现或改文档
措辞需另行决策。
