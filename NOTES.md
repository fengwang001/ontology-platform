# Deficit Round Robin 推导与验证

- flow 被发空并离开活动表时，赤字必须清零；只有“非空但队首块超过赤字”的 flow 才能把赤字带到下一轮。
- 非空受阻时，设队首块为 `h`，当前赤字 `d<h`；又因 `h<=MaxBlock`，所以 `d<=MaxBlock-1`。
- 下一轮补入 `quantum` 后，信用至多为 `quantum+MaxBlock-1`，因此它在这轮发出的字节数也不超过该上界。
- 若空 flow 错误保留赤字，离开前可残留至多 `quantum-1`；空闲再久不会继续增长，但回来后补一个 quantum，第一轮最多可发 `2*quantum-1`。
- 取 `quantum>MaxBlock` 且小块持续积压时，错误实现会发出 `2*quantum-1`，大于正确上界 `quantum+MaxBlock-1`；这正是空闲租户回归时的瞬时压制。
- 两个等权 flow 同时持续积压时，单轮超额字节 `<MaxBlock`，跨一轮的归一化发送量差至多 `2*MaxBlock-2`；测试用 `2*MaxBlock` 作为稳定断言。
