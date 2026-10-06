# cookie — 浏览器 Cookie 存储内核

结构化写入、请求附带判定、同站规则、过期处理与按站点/全局容量淘汰，
行为可精确复现。设计取舍见 [DESIGN.md](DESIGN.md)。

## 快速上手

```go
st := cookie.New(cookie.Config{
    SiteLimit:   150,            // 每站点条目数上限，0 为不限
    GlobalLimit: 3000,           // 全局条目总数上限，0 为不限
    LaxGrace:    2 * time.Minute, // 未声明 SameSite 的宽限时长
})

// 写入（结构化字段，不解析响应头）
err := st.Set(cookie.SetInput{
    Name: "sid", Value: "abc", Site: "a.com", Path: "/",
    Secure: true, SameSite: cookie.SameSiteLax,
    SourceSite: "a.com", SourceSecure: true,
})

// 时钟推进（回退被拒绝）
_ = st.AdvanceClock(time.Minute)

// 请求附带判定
entries, _ := st.Attach(cookie.Request{
    Site: "a.com", Path: "/x", Secure: true,
    InitiatorSite: "b.com", TopLevelNav: true, SafeMethod: true,
})

// 脚本读取（仅 HTTP 条目不可见）
visible, _ := st.ScriptRead("a.com", "/", true, nil)

// 清除与统计
st.ClearSite("a.com")
stats := st.Stats() // 过期淘汰 / 容量淘汰 / 显式删除 可区分查询
```

## 测试

```bash
go test ./cookie/ -v        # 单元测试 + 朴素模型差分
go test ./cookie/ -race     # 并发不变量
go test ./cookie/ -bench .  # 性能基准
```
