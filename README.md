# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双写缓冲批量页刷写与撕裂页修复（`doublewrite` 包）

根包实现了一个带双写缓冲的批量页刷写器。一批脏页只有两种结局：整批
全部生效为新版本，或整批停留在旧版本；任何页都不会出现新旧字节混合
（撕裂页）。

### 磁盘布局

一个 `Disk` 是按扇区提交的内存块设备，扇区写入是落盘的最小单位；
断电钩子 `BeforeSectorWrite` 可让某一扇区只落盘前若干字节（撕裂）或
整扇区不落盘。设备依次排布：

1. **原位区**：`NumPages` 个页，每页 `SectorsPerPage` 个扇区；
2. **双写区**：`BatchCapacity` 个页槽，与页等大；
3. **完成标记**：单独一个扇区，内容为 `magic(4)|count(4)|crc32(4)`。

每个页镜像为定长 `magic(4)|pageNo(4)|version(8)|payload|crc32(4)`，
CRC 覆盖除末尾 4 字节外的全部字节。任何扇区撕裂都会导致整页 CRC 失败。

### 写入顺序

`Flush` 持写锁（批次彼此严格串行），在写任何扇区前完成全部整批校验，
随后严格按顺序逐扇区提交：

1. 批内每一页整页写入双写区各自的页槽；
2. 双写区整批落盘后，写 **完成标记**（含批大小与自身 CRC）；
3. 标记落盘后才开始把每一页逐扇区写回原位；
4. 全部写回成功后清除标记，双写区退役。

### 完成标记的作用与恢复判定

`Recover` 读取标记：

- **标记有效**（magic、批大小范围、CRC 全部通过）：说明双写区是一次
  完整提交，对批内每页逐个判定：
  - 双写副本 CRC 通过，且原位 CRC 失败，或副本版本 **高于** 原位版本
    → 用副本整页 **前滚** 覆盖原位（原位校验通过但版本较低也覆盖）；
  - 副本 CRC 通过但原位版本 **不低于** 副本 → **原位不动**，因此双写区
    里比原位旧的残留副本绝不会把页回滚；
  - 副本不可读而原位有效（写回已完成）→ 原位不动；
  - 副本与原位都不可读 → 报告 **不可修复**。
  - 全部页面都有结论且无不可修复页后，清除标记。
- **标记无效或不存在**（含写标记前断电、标记扇区被撕裂、CRC 不符）：
  双写区 **整体忽略**，原位一律不动；原位 CRC 失败的页直接报告为
  **不可修复**。

不可修复页绝不猜测内容、不构造页镜像、不改任何字节；其版本按 **零**
计。可通过 `CorruptInPlacePage` / `CorruptDWAPage` / `CorruptMarker`
注入静默损坏（翻转单个字节）来触发该路径。

### 原子性、并发性与幂等

- 批次彼此串行生效；`ReadPage` 持读锁，与刷写并发时只会读到某个完整
  版本，不会读到撕裂页。
- 恢复可重复执行：成功前滚后标记已清除，第二次恢复找不到标记、不改变
  任何字节；存在不可修复页时字节同样保持稳定。
- 同一刷写序列与同一断电点产生逐字节相同的结果（测试中通过快照比对
  保证）。

### 整批拒绝（不写任何扇区）

以下错误在落盘前整体拒绝，返回可区分的哨兵错误：

- `ErrEmptyBatch`：空批次；
- `ErrBatchTooLarge`：批大小超过双写区容量；
- `ErrDuplicatePage`：同批页号重复；
- `ErrPageOutOfRange`：页号越界（或页镜像内嵌页号不合法）；
- `ErrInvalidPage`：页镜像长度/载荷非法；
- `ErrVersionNotNewer`：新版本不大于原位版本（原位已损坏的页按版本 0
  处理，允许新页修复覆盖）。

### 本地验证

```bash
# 全量测试（竞态检测）
go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 断电点遍历测试：逐扇区覆盖
#   双写区每个扇区边界、完成标记前后、原位每页每个扇区边界
go test -run TestPowerCutBoundary -v

# 观察一次断电后的输入/输出与逐页判定日志
go run ./cmd/dwdemo -cut 10            # 标记后、原位写回中断 -> 前滚
go run ./cmd/dwdemo -cut 2             # 标记前断电 -> 忽略双写区
go run ./cmd/dwdemo -cut 2 -corrupt-page 2   # 静默损坏 -> 不可修复
```

测试断言：每个断电点恢复后，批内所有页版本一致（全旧 1 或全新 2）、
非批内页不变、第二次恢复逐字节不变、相同序列与断电点两次运行逐字节
相同；并覆盖旧双写残留不得回滚、副本损坏但原位有效、以及静默损坏
不可修复等场景。日志统一打印 `input:` / `output:` / `decision:`。

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
