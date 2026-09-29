# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 沙箱内存文件树（`sandbox` 包）

`sandbox` 实现了带符号链接的内存文件树，以及保证不越出沙箱根的路径解析器。

### 节点与操作

- 节点分三类：目录（`KindDirectory`）、文件（`KindFile`）、符号链接（`KindSymlink`）；链接只保存原始目标文本，目标可为绝对（以 `/` 开头）或相对路径。
- `New()` 创建只含根目录的树；`Mkdir` / `CreateFile` / `Symlink` / `Remove` / `Rename` 执行变更，`Resolve(path, followFinal)` 执行解析。
- 末段是否跟随链接由 `followFinal` 决定（`false` 即 lstat 语义，直接返回链接节点本身）。

### 逐段解析与点点的物理语义

解析把路径切分为段后逐段处理，维护一个“真实目录栈”：

- 空段（连续斜杠）与 `.` 段忽略；段必须在当前真实目录中按名字查找，不存在返回 `ErrNotExist`，中间段不是目录返回 `ErrNotDirectory`。
- `..` 弹出栈顶，即回到**当前物理所在目录**的父目录。因此经过符号链接后的 `..` 回到链接**目标**的父目录，而不是链接所在目录：链接指向 `x/y` 时，`a/链接/..` 解析为 `x`。
- 根处的 `..` 停在根，栈不会被弹空。
- 中间段遇到链接总是跟随：绝对目标以沙箱根为起点（清空目录栈），相对目标相对于**链接所在目录**展开；目标段注入到待处理队列前部，随后继续逐段解析。

### 根边界与跟随上限

- 解析结果始终是树内真实节点；不存在“沙箱之外”这一概念，绝对链接目标与 `/../...` 均以沙箱根为唯一起点，根处 `..` 被钳制。
- 单次解析累计跟随符号链接**恰好 40 次仍成功，第 41 次返回 `ErrTooManyLinks`**；自指与互指链接因此被判定为循环。

### 可区分的拒绝原因

所有变更操作先完整校验、再修改树，被拒绝时树不发生任何变化。错误均为哨兵值，可用 `errors.Is` 区分：

`ErrEmptyPath`（路径为空）、`ErrInvalidName`（名字为空或含斜杠）、`ErrNotExist`（段不存在）、`ErrExists`（创建时名字已存在）、`ErrNotDirectory`（中间段不是目录）、`ErrDirectoryNotEmpty`（删除非空目录）、`ErrInvalidArgument`（如空链接目标）、`ErrTooManyLinks`（跟随超限/循环）、`ErrRenameIntoSelf`（目录移到自身或子孙之下）。

### 并发语义

- 解析取读锁、变更取写锁；一次解析在单个锁快照内完成，改名在写锁内原子完成。
- 因此每次解析的结果必然等于某次改名**前**或改名**后**的整棵树串行解析结果，不会出现任何时刻都不存在的中间位置；树静止时同一路径反复解析结果完全相同。

### 本地验证

```bash
# 常规测试
go test -v ./sandbox

# 竞态检测 + 重复执行（含并发改名测试）
go test -race -count=30 ./sandbox

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试用 `-v` 日志逐条打印输入、输出与判定依据，覆盖：`a/链接/..` → `x`（绝对/相对目标两种链接）、多个 `..` 越根停根、绝对链接留在沙箱内、自指与互指链接、恰好 40 次跟随成功与第 41 次失败、目录移入子孙被拒且树不变、并发改名下的解析只能取改名前/后串行状态。
