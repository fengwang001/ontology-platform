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

## 依赖注入容器（`di` 包）

`di` 包提供带生命周期、依赖解析与关闭释放的并发安全依赖注入容器。

### 基本用法

```go
c := di.New()
c.Register("db", nil, di.Singleton, func(deps map[string]any) (any, func()) {
    db := openDB()
    return db, db.Close // 第二个返回值为可选的释放函数
})
c.Register("orderService", []string{"db"}, di.Scoped, newOrderService)
if err := c.Freeze(); err != nil { // 校验通过后才允许解析
    log.Fatal(err)
}

scope, _ := c.NewScope()
svc, _ := scope.Resolve("orderService")
defer scope.Close()
defer c.Close()
```

注册项包含服务名、依赖名列表、生命周期与构造函数
`func(deps map[string]any) (instance any, release func())`；`release`
可为 `nil`，由容器保证恰好调用一次。

### 三种生命周期

- **单例（`Singleton`）**：每个容器至多**成功**构造一次，失败不缓存、下次解析重新构造。单例（含其构造链上新建的瞬态）由容器在关闭时释放。
- **作用域（`Scoped`）**：每个作用域至多成功构造一次，同一作用域内菱形依赖共享同一实例，跨作用域各自独立。从**根容器**直接或间接解析作用域服务一律被拒（`ErrScopedFromRoot`），必须先 `NewScope()`。
- **瞬态（`Transient`）**：每次解析都新建。随其所在的构造链归属：被单例俘获的随容器释放，被作用域实例俘获的随作用域释放，顶层解析的归根或当前作用域。

### 冻结与俘获判定

`Freeze()` 对整批注册做静态校验，**只报第一个**问题并整体拒绝；冻结失败后容器仍可继续注册修正。校验顺序固定为：

1. **重复注册**（`ErrDuplicateRegistration`）；
2. **依赖未注册**（`ErrDependencyNotFound`）；
3. **依赖成环**（`ErrDependencyCycle`，附带环路径）；
4. **生命周期俘获**（`ErrCaptiveDependency`）。

俘获判定的展开规则：从每个单例出发沿依赖闭包展开——**穿过瞬态继续展开，遇到单例即停止**（该单例自己的闭包由它自身负责校验）；闭包中只要出现作用域服务就拒绝。因此 `singleton -> transient -> scoped` 会被拒，而 `singleton -> singleton -> ...` 与 `transient -> singleton` 合法。

### 构造失败与释放

- 构造函数返回错误或 panic 时，本次解析新建的实例全部按**创建逆序**释放，单例/作用域失败不写缓存，下次解析重新构造；此前已成功的单例保留。
- 正常释放遵循同一条规则：**依赖者先于其依赖释放**，每个实例恰好一次。
- 作用域关闭：拒绝关闭开始后发起的解析（`ErrScopeClosed`），等待在途解析结束，再逆序释放该作用域创建的实例。
- 容器关闭：先关闭全部子作用域（在途解析完成、各自逆序释放），再逆序释放根拥有的单例与归根瞬态。
- 关闭可重入且幂等；关闭已完成后的解析/建作用域返回 `ErrContainerClosed`。

### 并发语义

- `Resolve`、`NewScope`、`Close` 均可并发调用，内部由互斥锁 + 条件变量 + 单飞（single-flight）通道协调。
- 并发首次解析同一单例：构造函数只执行一次；成功者拿到同一实例，失败者同批等待者得到**同一个错误**。作用域实例在作用域粒度上同样单飞。
- 关闭与解析并发时，关闭只等待已开始的解析，不允许关闭开始后新解析“挤进”并创建实例；被拒绝的操作不创建任何实例。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测（关闭与解析并发、百 goroutine 单例单飞等用例）
go test -race -count=3 ./di

# 详细日志：每个用例打印输入、输出与判定依据
go test -race -v ./di

go vet ./...
gofmt -l .
```

测试位于 `di/container_test.go`，覆盖：单例经瞬态依赖作用域被拒、校验顺序、根解析作用域被拒、菱形依赖共享作用域实例、第三层构造失败逆序释放、单例失败保留已成功单例并重试、100 个 goroutine 并发首次解析单例、并发失败同错、作用域/容器关闭与解析并发等。
