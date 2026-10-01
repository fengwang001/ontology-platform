# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## A/B 双分区固件升级状态机

根包提供 `UpgradeManager`，用槽 0 与槽 1 管理 A/B 分区升级。构造函数为 `NewUpgradeManager(v0, M)`，要求 `v0 >= 1`、`M >= 1`。

### 槽状态

- `SlotEmpty`：空槽，版本为 0，可被安装覆盖。
- `SlotGood`：已确认可用；活动槽始终处于该状态。
- `SlotTrial`：新版本已写入，正在按剩余次数试启动。
- `SlotBad`：试启动次数耗尽且未确认；可被更高版本安装覆盖。

初始时槽 0 为 `Good`、版本 `v0` 且是活动槽；槽 1 为 `Empty`、版本 0；版本下限 `floor = v0`。

### 操作规则

- `Install(v)`：写入非活动槽并置为 `Trial`，剩余试启动次数为 `M`。拒绝顺序是：`v == 0`、`v <= floor`、非活动槽已经是 `Trial`。因此版本等于 `floor` 也视为降级；确认成功后同版本不能再次安装。
- `Boot()`：非活动槽为 `Trial` 且剩余次数大于 0 时，先减 1，再从该槽试启动；剩余次数为 0 时，本次启动将该槽置为 `Bad`，并回滚到活动槽，返回 `Rollback: true`。其他情况都从活动槽正常启动。
- `Confirm()`：只有上一次启动是试启动时有效。确认会把试启动槽置为 `Good`、切换活动槽，并把 `floor` 提升到该槽版本；原活动槽保持 `Good`。
- `Snapshot()`：无错误地返回两个槽、活动槽、`floor` 与“上次启动是否为试启动”的一致快照。

当 `M=3` 时，第 1～3 次 `Boot()` 都是试启动，第 4 次 `Boot()` 才回滚；`M=1` 时第 2 次 `Boot()` 回滚。回滚不改变 `floor`。被置为 `Bad` 的槽上，只要新版本严格大于当前 `floor`，就可以再次安装。

所有公开操作都由同一个互斥锁保护；`Boot()` 和查询不会失败，任何时刻都满足：

- `floor == 活动槽版本`，且 `floor` 永不下降。
- 活动槽状态始终为 `Good`。
- `Trial` 槽版本严格大于 `floor`。

可区分的错误为：

- `ErrInvalidConstructorArgs`：构造参数非法。
- `ErrInvalidVersion`：安装版本为 0。
- `ErrVersionNotRaised`：安装版本不大于 `floor`。
- `ErrInactiveSlotOnTrial`：非活动槽仍处于试启动。
- `ErrNoTrialBoot`：上次启动不是试启动却调用确认。

示例：

```go
manager, err := ontology.NewUpgradeManager(3, 2)
if err != nil {
    log.Fatal(err)
}

if err := manager.Install(4); err != nil {
    log.Fatal(err)
}

first := manager.Boot()  // 槽 1，版本 4，Trial=true，剩余次数 1
second := manager.Boot() // 槽 1，版本 4，Trial=true，剩余次数 0
third := manager.Boot()  // 槽 0，版本 3，Rollback=true
```

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

# 查看状态机逐步输入、输出、状态快照与判定依据
go test -v -run 'TestRulesAgainstNaiveSimulation|TestRollbackOccursOnBootMPlusOne'

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
