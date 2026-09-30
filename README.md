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

## vfs：带符号链接的沙箱内存文件树

`vfs` 包提供一棵并发安全的内存文件树（目录 / 文件 / 符号链接）及其
沙箱路径解析器，任何解析结果都不会越出根目录。

### 逐段解析与物理 `..` 语义

- 路径按 `/` 逐段解析；空段与 `.` 被忽略。
- `..` 回到**当前真实所在目录**的父目录：经过符号链接后，`..` 回到
  链接目标的父目录，而非链接所在目录。例如链接 `a/link -> x/y` 时，
  `a/link/..` 解析为 `x`。
- 根处的 `..` 停在根，多个 `..` 越过根也停在根。

### 根边界与符号链接

- 以 `/` 开头的路径与链接目标均以沙箱根为起点，解析不会越出根目录。
- 相对链接目标相对于链接所在目录解析。
- 中间段的链接总是跟随；末段是否跟随由 `Resolve(path, followLast)` 的
  `followLast` 参数指定。
- 单次解析累计跟随超过 **40** 次（`MaxFollows`）即判为循环，返回
  `ErrTooManyLinks`；恰好 40 次成功。

### 并发与串行化保证

解析、创建、删除与改名可并发调用。所有操作经全局读写锁串行化：
并发改名下，每次解析的结果总等于改名前或改名后某一时刻整棵树上的
串行解析结果，不会解析出任何时刻都不存在的位置；同一棵树与同一路径
反复解析结果完全相同。

### 可区分的拒绝原因

被拒绝的操作整体生效前校验，不改变树。错误均为哨兵错误，可用
`errors.Is` 区分：`ErrEmptyPath`（路径为空）、`ErrNotDir`（中间段不是
目录）、`ErrNotExist`（段不存在）、`ErrTooManyLinks`（跟随超限）、
`ErrInvalidName`（名字为空或含斜杠）、`ErrExist`（创建时名字已存在）、
`ErrDirNotEmpty`（删除非空目录）、`ErrMoveIntoSelf`（把目录移到其自身
或子孙之下）、`ErrRoot`（删除或移动根目录）。

### 本地验证

```bash
# 运行 vfs 全部测试（日志含每个用例的输入、输出与判定依据）
go test -v ./vfs

# 并发场景需配合竞态检测
go test -race -v ./vfs
```
