# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 版本清单（MANIFEST）管理器

`ontology` 包实现带快照与增量编辑回放的存储版本清单管理器。版本含 0..6 共 7 层，
每个文件为 `File{Level, Num, Smallest, Largest}`：`Num` 为正整数；键为非空字节串，
按字节序比较且 `Smallest ≤ Largest`。版本另有 `LogNumber`、`NextFile`、`LastSeq`
三个无符号整数。第 0 层文件按 `Num` 从大到小排列、允许重叠；第 1..6 层按
`Smallest` 升序排列，同层文件闭区间不得重叠（首尾恰相接也算重叠）。

### 创建与初始状态

- `New(T)`：`T < 1` 返回 `ErrParam`。初始版本为空，`LogNumber=0`、`NextFile=2`、
  `LastSeq=0`；内存磁盘上已有清单 1，含一条 `Snapshot` 记录，`CURRENT` 指向 1。
- 清单是按序追加的记录列表；`Record` 为 `Snapshot`（完整版本）或 `Edit`，
  并带 `Torn` 标记表示只写了一半。

### Apply 的编辑校验次序

`Apply(e)` 的编辑含 `Adds`、`Dels`（只需层与 `Num`）以及三个可选指针
（`LogNumber`、`NextFile`、`LastSeq`，nil 表示未给出）。严格按下列次序检查，
报第一个成立的错误，成功才整体生效：

1. `ErrParam`：层越界、`Num=0`、键为空、`Smallest > Largest`；
   `Adds`、`Dels` 皆空且三个指针皆 nil（空编辑）。
2. `ErrNoFile`：按 `Dels` 顺序从当前版本逐层删除，找不到即报。
3. `ErrDupFile`：删除之后，某个 Add 的 `Num` 已存在于任一层，或与本编辑内
   更早的 Add 同号。同编辑内“先删后加同号”因此成功。
4. `ErrRegress`：给出的 `NextFile`、`LogNumber`、`LastSeq` 小于当前值（恰等允许）；
   未给出的字段不参与判定。
5. `ErrLogAhead`：有效 `NextFile = max(当前值, 给出的 NextFile, 所有 Add 的 Num+1)`，
   给出的 `LogNumber` 必须严格小于该有效值；`LogNumber` 恰等于有效值即报错。
6. `ErrOverlap`：编辑后第 1..6 层存在重叠。每个新增文件只与插入位置的前驱和
   后继比较（非导出探针计数 `overlapProbes ≤ 2×|Adds|`）。

生效时先删后加；`NextFile` 取上述有效值（给出值不大于 Add 的 `Num+1` 时静默抬高），
`LogNumber`/`LastSeq` 仅在给出时更新。编辑原样追加到当前清单；若追加后记录数
大于 `T`（恰等不轮转），立即原子地再执行一次 `Rotate`。被拒绝的编辑不改版本、
不追加记录。

### 快照与轮转

- `Rotate()`：`M = 当前 NextFile`，`NextFile` 加 1，新建清单 `M`，其唯一记录为
  `Snapshot`（含全部文件与 `LogNumber`、**加 1 之后**的 `NextFile`、`LastSeq`），
  然后 `CURRENT` 指向 `M` 并返回 `M`。
- `RotateUnflipped()`：做相同的编号消耗与新清单写入（活动版本 `NextFile` 同样加 1），
  但不改 `CURRENT`，用来模拟换清单中途崩溃；之后的编辑仍追加到旧清单。
- `Disk()` 返回磁盘深拷贝（清单号 → 记录列表、`CURRENT`）；可在返回值上把某条
  记录标记为 `Torn` 来模拟半截写入。`View()` 返回版本深拷贝，层顺序符合上述约定。

### 恢复规则

`Recover(d)` 按序回放 `CURRENT` 指向的清单：

- 清单不存在报 `ErrNoCurrent`；首条必须是未撕裂的 `Snapshot`，否则报 `ErrCorrupt`。
- 之后再出现 `Snapshot` 报 `ErrCorrupt`。
- 撕裂的 `Edit`：是最后一条（其后无记录）则忽略；不是最后一条报 `ErrCorrupt`。
- 未撕裂的 `Edit` 按 Apply 的生效逻辑回放（`NextFile` 同样取有效值，
  `LogNumber`/`LastSeq` 仅在给出时更新），但**只做第一、二、三、六步检查**
  （孤立清单会让回放时的 `NextFile` 小于写入时，故不检 `ErrRegress`/`ErrLogAhead`）。
  任何检查失败都返回 `*CorruptError`：带记录下标，`errors.Is` 同时命中
  `ErrCorrupt` 与具体原因（如 `ErrOverlap`）。
- 恢复得到的 `NextFile = max(回放所得, 磁盘上最大清单号+1)`，使崩溃遗留的
  孤立清单编号不被复用。

`Open(d)` 在 `Recover` 成功后以恢复版本为活动版本，并立即 `Rotate` 一次得到
新清单；旧清单原样保留，磁盘使用输入的深拷贝。

所有方法均持锁实现，可并发调用，结果等价于某个串行顺序；`Apply` 与其触发的
`Rotate` 是同一个原子步骤。相同操作序列重放得到完全相同的清单与版本。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 仅验证版本清单管理器（含 2000 组随机编辑序列与朴素模型对照；
# -v 可查看每步的输入、输出与判定依据）
go test -v ./ontology

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
