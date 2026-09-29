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

## 依赖注入容器（`di` 包）

`di` 包实现了一个带生命周期、可并发使用的依赖注入容器，位于 `di/`。

### 快速上手

```go
c := di.New()
c.Register(di.Registration{
    Name:         "db",
    Lifetime:     di.Singleton,
    Dependencies: []string{"cfg"},
    Construct: func(deps map[string]any) (any, error) {
        return &DB{cfg: deps["cfg"].(*Config)}, nil
    },
})
c.Register(di.Registration{Name: "cfg", Lifetime: di.Singleton, Construct: loadConfig})
if err := c.Freeze(); err != nil {   // 冻结时完成全部静态校验
    log.Fatal(err)
}
defer c.Close()

scope, _ := c.NewScope("request-1")
defer scope.Close()
v, err := scope.Resolve("db")
```

实例若实现 `di.Disposer`（`Dispose()` 方法），容器会在关闭/回滚时调用它。

### 三种生命周期

- `Singleton`：每个容器至多**成功构造一次**，由根作用域拥有并缓存，跨所有子作用域共享。
- `Scoped`：每个作用域至多成功构造一次，由创建它的作用域拥有并缓存；作用域之间不共享。从根作用域解析作用域服务会被拒绝（`ErrScopedFromRoot`），且不创建任何实例。
- `Transient`：每次顶层解析都新建；同一次解析图内的菱形瞬态边共享同一实例。归属规则：根解析产生的瞬态归根作用域（随容器关闭释放）；作用域解析产生的瞬态（含其俘获闭包）随该作用域释放。

### 冻结校验

`Freeze()` 按以下固定顺序校验，**只报第一个错误并整体拒绝冻结**；校验失败不会冻结容器，可继续 `Register` 后再次 `Freeze`：

1. 重复注册（按注册顺序找到第一个重名项）；
2. 依赖未注册；
3. 依赖成环；
4. 生命周期俘获。

俘获判定的展开规则（从每个单例出发遍历依赖闭包）：

- 依赖是**瞬态**：继续向其依赖展开；
- 依赖是**单例**：在此边停止展开（该单例有自己独立、已被保证无俘获的闭包）；
- 依赖是**作用域服务**：判定为俘获并拒绝（例如“单例 → 瞬态 → 作用域”会被拒）。

### 构造失败与释放顺序

- 构造函数在其全部依赖构造成功后才调用；构造失败时，本次解析新建且未被已成功单例保留的实例，按**构造成功顺序的逆序**释放，且不写入任何缓存。
- 失败当次已经构造成功的单例**保留**（连同它俘获的闭包），下次解析直接复用；失败的单例不缓存，下次解析重新构造。
- 被拒绝的操作（未冻结、已关闭、根解析作用域服务等）不会创建任何实例。
- 任何实例被释放时，依赖它的实例都已经先释放：同一归属作用域内依赖先构造完成，关闭时按构造顺序逆序释放，因此依赖者总是先于被依赖者释放。

### 关闭语义

- `Scope.Close()`：拒绝其后开始的解析（`ErrScopeClosed`），等待本作用域全部在途解析结束，然后逆序释放该作用域拥有的实例（作用域服务与作用域内瞬态）。重复关闭幂等。
- `Container.Close()`：先关闭全部子作用域，再关闭根作用域（逆序释放单例与归根瞬态）。因此单例一定在一切作用域实例之后释放。重复关闭幂等；关闭后任何解析返回 `ErrContainerClosed`。
- 每个实例恰好释放一次。

### 并发语义

- `Resolve` 与 `Close` 可被并发调用，全部内部状态带锁保护。
- 并发首次解析同一单例时只执行一次构造：同批等待者阻塞至构造完成，成功时拿到同一实例；失败时拿到**同一个错误**，且失败不缓存，下一批解析重新构造。
- 作用域服务在每个作用域内有同样的单飞语义；菱形依赖在同一解析内共享同一实例。
- 作用域关闭与在途解析并发时：关闭等待在途解析结束后再释放，期间已进入的解析可以正常完成；关闭标记之后才开始的解析立即被拒。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 多轮重复 + 详细日志（日志含每个用例的输入、输出与判定依据）
go test -race -count=3 -v ./di

# 静态检查与格式
go vet ./...
gofmt -l .
```

关键用例：

- `TestFreezeValidationOrder`：校验错误优先级，含“单例经瞬态依赖作用域被拒”；
- `TestLifetimesAndCaching`：三种生命周期、菱形依赖共享作用域实例、根解析作用域被拒；
- `TestConstructFailureRollsBackInReverse`：第三层构造失败后逆序释放、失败不缓存、重试重建；
- `TestSuccessfulSingletonSurvivesSiblingFailure`：已成功单例在兄弟失败时保留；
- `TestConcurrentFirstSingletonResolution`：100 个并发首次解析只构造一次；
- `TestConcurrentSingletonFailureSharesError`：并发失败同批同一错误；
- `TestCloseConcurrentWithResolve` / `TestContainerCloseConcurrentWithScopedResolve`：关闭与解析并发；
- `TestTransientOwnershipAndReleaseOnce` / `TestDiamondScopedSharedDisposedOnce`：归属、恰好释放一次。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
