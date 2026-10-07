# sw — 服务 Worker 注册与版本更新协调器

模拟浏览器 Service Worker 的注册、版本生命周期、客户端控制、更新检查与缓存清单，
用于精确复现多页面客户端在多次版本更新、等待、接管与注销下的控制关系。

## 快速开始

```go
c := sw.NewCoordinator(sw.Config{MinUpdateInterval: 10 * time.Second})
now := time.Now()

c.Register("/app/", "sw.js", "script-content", now)   // 作用域必须以 / 结尾
c.CheckUpdate("/app/", "script-content", now)         // 摘要不同才产生新版本（安装中）
c.InstallSucceeded("/app/", 1, sw.InstallOpts{}, now) // 安装成功：激活位为空则立即激活

c.Navigate("tab-1", "/app/index", now)                // 最长前缀归属，被激活版本控制
ans, _ := c.Request("tab-1", "/app/data")             // 按受控版本的清单作答
c.NavigateAway("tab-1", now)                          // 离开即解除控制
```

## 行为要点
- 每个注册最多持有 安装中/等待中/激活中 三个版本；新安装成功会顶替旧等待版本，冗余为终态。
- 激活位被占时，新版本等待到激活版本客户端数归零，或 `InstallOpts.SkipWaiting` / `SkipWaiting` 显式跳过。
- `InstallOpts.ClaimClients` 在接管瞬间转移注册内全部客户端；否则旧客户端终身受控于原版本。
- `InstallOpts.InheritManifest` 在接管瞬间一次性继承旧激活版本清单中未声明的条目。
- 更新检查按脚本内容摘要判定；距上次成功检查不足 `MinUpdateInterval` 拒绝为 `ErrTooFrequent`，
  脚本地址变化触发的检查不受限。
- 注销进入待移除：不接受新归属与更新检查，最后受控客户端离开时整体冗余；期间重新注册可复活。
- 错误类别：`ErrInvalidArgument`、`ErrClockRollback`、`ErrRegistrationNotFound`、
  `ErrVersionNotFound`、`ErrStateNotAllowed`、`ErrTooFrequent`，按此次序拒绝，被拒绝的操作不产生任何副作用。

设计取舍与验证方法见 [DESIGN.md](DESIGN.md)。
