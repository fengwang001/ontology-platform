# 快照库边界行为与文档承诺差异

以下结论由 `characterization_test.go` 钉住，均为**当前真实行为**（测试全部通过），
不代表期望行为。未改动任何实现文件。

## 1. 长度前缀分类依赖固定 64MB 绝对阈值，而非「是否超出文件余量」

- **位置**：`read.go` 的 `scanFrame`，常量 `maxBodySize = 64 << 20`（`format.go`）。
- **分支条件**：先算 `total = 4 + claimed + 4`；仅当 `total > len(body)-pos`
  （claim 塞不进剩余字节）时进入分类，然后 `claimed > 64MB` 才报
  `ErrLengthTooLarge`，否则一律报 `ErrRecordTruncated`。
- **复现输入**：两记录文件，记录区共 50 字节（首帧剩余 46 字节）。把首记录
  长度前缀分别改成：
  - `0x00010000`（64KB，远小于 64MB，但远超 46 字节余量）；
  - `0x0000FFFF`/`0x01000000`（64MB-1、64MB 本身）；
  - `0x01000001`（64MB+1）；
  - `0xFFFFFFFF`。
- **实际输出**：64KB、64MB-1、64MB 三种虚报都返回
  `ErrRecordTruncated`；只有 `claimed > 64MB` 的两档返回 `ErrLengthTooLarge`。
  `Inspect` 同样：64KB 档 `BadErr=ErrRecordTruncated`，0xFFFFFFFF 档
  `BadErr=ErrLengthTooLarge`，两者 `BadIndex=0`、`Skipped=1`、`RegionErr=true`、
  `CountErr=nil`（坏点处仍有剩余字节，RecordError 优先于 CountMismatch）。
- **应当输出**：`ErrLengthTooLarge` 的注释是「length prefix exceeds the
  remaining file」，按语义凡是 claim 超出剩余字节都应归入此类；截断应只用于
  文件物理变短（如 `TestMidRecordTruncation` 那种文件被切断、原本合法的帧落
  到 EOF）。当前用固定绝对阈值做二次切分，使「同一种虚报长度」按绝对大小被
  拆成两个错误类，`errors.Is(err, ErrLengthTooLarge)` 探测会漏掉 64KB 档。
- **根因**：分类条件把「claim 是否离谱（绝对大小）」误当成「claim 是否真的
  塞不进（相对余量）」；`format.go` 注释只说「far exceeds any remaining
  file」，未说明该阈值与实际余量无关、是硬编码分档。

## 2. `Read` 不校验记录是否按键排序

- **文档承诺**：包注释称文件包含「primary-key ordered records」，`Write` 文档
  也称记录按主键排序；`Write` 确实用 `sort.SliceStable` 排序后写盘。
- **复现输入**：用 `buildRaw`（绕过 `Write`）写 CRC 全部正确、区域 CRC 也正确、
  但键序为 `charlie, alpha, bravo`（以及严格降序 `c,b,a`）的文件。
- **实际输出**：`Read` 返回 `nil` 错误，记录按**文件字节顺序**原样返回
  （`charlie, alpha, bravo`），不报错、不重排。`Inspect` 同样给出全干净报告：
  `BadIndex=-1`、`BadErr=nil`、`Skipped=0`、`CountErr=nil`、`RegionErr=false`，
  `Records` 即乱序输入。
- **应当输出**：若「ordered」是文件格式不变量，读侧应在
  `keys[i] < keys[i-1]` 时返回携带索引的错误；否则应在文档中明确排序仅是
  「写侧尽力保证」，读侧不验证。
- **根因**：`scanFrame`/`Read` 解码循环里完全没有 `lastKey` 之类的相邻键
  比较；排序逻辑只存在于 `write.go`，读侧无对应检查，文档未界定责任归属。

## 3. 同主键重复记录不被拒绝

- **文档承诺**：`Record` 文档称「one snapshot entry keyed by a primary key
  string」，「primary key」隐含唯一；且 `Write` 排序以 Key 为首要键，未去重。
- **复现输入**：CRC 全部正确的两记录文件，两条键均为 `"dup"`（值/note 不同），
  以及 `"dup","dup","mid"` 三记录文件。
- **实际输出**：`Write` 不查重，照写两条；`Read` 返回 `nil` 错误且返回**全部**
  同键记录（2 条/3 条）。`Inspect` 同样报告干净，`BadErr=nil`、`Skipped=0`、
  `CountErr=nil`、`RegionErr=false`。下游若以 `Key` 建 map 将静默覆盖先出现的
  一条。
- **应当输出**：要么 `Write` 拒绝/显式处理重复主键，要么 `Read`/`Inspect` 对
  相邻同键返回可 `errors.Is` 分类的冲突错误；至少文档应说明允许重复及覆盖语义。
- **根因**：写侧排序比较器把同键视为合法并列（继续比 Value/Note），读侧无任何
  集合/相邻键唯一性检查；唯一性从未在代码中被强制，仅停留在文档措辞。
