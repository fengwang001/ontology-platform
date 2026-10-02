# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 存储版本清单（MANIFEST）管理器

`ontology` 包实现了带快照与增量编辑回放的存储版本清单管理器，包含内存模拟磁盘
（`Disk`：清单号 → 记录列表，以及 `CURRENT` 指针）。版本包含 0..6 共 7 层文件，
每个文件为 `File{Level, Num, Smallest, Largest}`，另有 `LogNumber`、`NextFile`、
`LastSeq` 三个无符号整数指针。第 0 层文件按 `Num` 降序排列且允许重叠；第 1..6 层按
`Smallest` 升序排列，闭区间不得重叠（首尾相接，即 `Largest == Smallest` 也算重叠）。

### 初始状态

- `New(T)` 要求 `T >= 1`，否则返回 `ErrParam`。
- 初始版本为空：`LogNumber=0、NextFile=2、LastSeq=0`。
- 磁盘上已有清单 1，唯一记录为初始版本的 `Snapshot`，`CURRENT` 指向 1。

### Apply 编辑校验次序

记录是 `Snapshot`（完整版本）或 `Edit`。`Apply(e)` 严格按以下次序校验，报第一个成立的
错误；只有全部通过才整体生效，并把编辑原样追加到当前清单。被拒绝的编辑既不改版本也
不追加记录：

1. `ErrParam`：层越界（不在 0..6）、`Num==0`、键为空、`Smallest > Largest`，或
   `Adds、Dels` 皆空且三个可选指针（`LogNumber、NextFile、LastSeq`，`nil` 表示未给）
   都未给的空编辑。
2. `ErrNoFile`：按 `Dels` 顺序从当前版本删除，任一条找不到即报错。
3. `ErrDupFile`：删除之后，某个 Add 的 `Num` 已存在于任一层，或与本编辑内更早的 Add
   同号。语义上删除永远先于添加：同编辑内先删后加同号成功，而 Add 一个版本中仍存在
   的号（未先删除）报 `ErrDupFile`。
4. `ErrRegress`：给出的 `NextFile、LogNumber、LastSeq` 小于当前值（恰等允许）；未给的
   字段不参与判定。
5. `ErrLogAhead`：有效 `NextFile = max(当前值, 给出的 NextFile, 所有 Add 的 Num+1)`，
   给出的 `LogNumber` 必须严格小于它，等于即报错。
6. `ErrOverlap`：编辑后第 1..6 层存在闭区间重叠。每个 Add 只与其插入位置的前驱、后继
   比较，非导出探针计数满足 `overlapProbes <= 2 * len(Adds)`（可通过 `OverlapProbes()`
   查看最近一次 Apply 的计数）。

生效规则：先删后加；`NextFile` 取上述有效值（给出值不足以覆盖 Add 时静默抬高，不报错）；
`LogNumber、LastSeq` 仅在给出时更新。追加后若当前清单记录数 **大于** `T`（恰等不轮转），
立即在同一个原子步骤内执行一次轮转。所有方法可并发调用，`Apply` 连同其触发的轮转
对其他调用者表现为一个不可分割的串行步骤。

### 快照与轮转

- `Rotate()`：`M = 当前 NextFile`，活动版本 `NextFile` 加 1；新建清单 `M`，其唯一记录
  是含全部文件和指针（快照中的 `NextFile` 为加 1 之后的值）的 `Snapshot`；随后把
  `CURRENT` 指向 `M`，返回 `M`。
- `RotateUnflipped()`：做相同的编号消耗与新清单写入（活动版本 `NextFile` 同样加 1），但
  不修改 `CURRENT`，用于模拟“换清单中途崩溃”；之后的编辑仍追加到旧清单，新清单成为
  磁盘上的孤立清单。
- `Disk()` 返回磁盘深拷贝；记录带 `Torn` 标记，表示只写了一半。`View()` 返回版本深拷贝。

### 恢复规则（Recover / Open）

`Recover(d)`：

- `CURRENT` 指向的清单不存在：`ErrNoCurrent`。
- 首条记录必须是未撕裂的 `Snapshot`，否则 `ErrCorrupt`。
- 首条之后再出现 `Snapshot`：`ErrCorrupt`。
- 撕裂的 `Edit`：是最后一条（其后无记录）则忽略；不是最后一条则 `ErrCorrupt`。
- 未撕裂的 `Edit` 按 Apply 的生效逻辑回放，但只执行第一、二、三、六步检查；第四、五步
  依赖写入时刻的活动编号，孤立清单会让回放时的 `NextFile` 小于写入时，因此不检查，
  `LogNumber、LastSeq` 直接取给出值。任何检查失败都报 `ErrCorrupt`。
- 错误为 `*RecoverError`，同时 `errors.Is` 到 `ErrCorrupt` 与具体原因，并携带记录下标
  （`Index`，从 0 开始）。
- 恢复得到的 `NextFile = max(回放所得, 磁盘上最大清单号+1)`，保证崩溃遗留的孤立清单
  编号不会被复用。

`Open(d)` 先 `Recover`，以恢复版本为活动版本（磁盘保留原样），随后立即 `Rotate` 一次
生成新清单；轮转阈值使用包常量 `DefaultThreshold`。

### 本地验证

```bash
# 若 GOCACHE 默认目录只读，可指向 /tmp
export GOCACHE=/tmp/gocache

# 全量测试（含 2000 组随机编辑序列与朴素模型的差分对照）
go test ./...

# 竞态检测 + 详细输出（随机序列每组打印输入编辑、接受/拒绝与判定依据）
go test -race -v ./ontology

# 仅跑随机差分对照与恢复用例
go test ./ontology -run 'TestRandomDifferential2000|TestRecover|TestRotateUnflipped' -v

# 静态检查与格式化
gofmt -l .
go vet ./...
```

随机差分测试（`TestRandomDifferential2000`，固定种子可精确复现）在 40 组 × 50 步序列上，
逐步对照按同一规格独立编写的朴素模型：比较 Apply 的错误哨兵、活动版本，以及
`Recover(Disk())` 与 `View()`、与模型恢复结果的一致性，并检查 `overlapProbes` 上界。

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
