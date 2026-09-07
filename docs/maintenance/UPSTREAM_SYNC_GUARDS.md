# 上游同步时必须保持的本地行为

## 使用原则

这是本分支的维护清单。每次同步上游都读取本文件，识别受影响条目，再比较实现与运行相关回归测试。个人记忆和历史聊天用于找到清单；当前源码、测试和本文件共同提供审查依据。

用户约定：除非上游具有等价或更好的实现，否则以本地修复为准。“更好”需要覆盖下列行为，并给出正确性、并发安全、兼容性或有证据的性能收益；提交更新、代码更短或合并无冲突都不能单独证明更好。

本清单目前登记 2026-09-08 的 URLTest 修复，尚未穷举分支的全部增强功能。未列出的本地功能也不得在同步中随意丢弃。

## 已核对的修复基线

- 内核：[047febf578f4133ca1bbebceae6ce3134cdc864a](https://github.com/isdzjfs/sing-box/commit/047febf578f4133ca1bbebceae6ce3134cdc864a)，父提交为本次上游同步 `c7f858aaf7bf1b07cc678e8d1e5598706ffa3565`。
- SenVPN：[36389f22ab129a75903c66a2ef2695183379d1bd](https://github.com/isdzjfs/SenVPN/commit/36389f22ab129a75903c66a2ef2695183379d1bd)，其 `core/libbox/version.properties` 在此基线固定到上述内核提交。
- 原修复任务报告：内核回归测试及竞态检测通过，SenVPN 246 项单元测试通过，生成了 x86_64 AAR 和调试 APK；未做真机验证。这是历史验证记录，不代替后续同步后的验证。
- 哈希仅用于追溯修复，不要求以后固定在这些版本。更新内核仓库不等于更新 SenVPN 使用的 AAR 或部署到设备。

## URLTest 行为清单

以下条目均为有效约束。路径相对于本仓库；标有 `SenVPN` 的路径属于配套 Android 仓库。

| 编号 | 必须保持的行为与典型反例 | 主要实现入口 | 回归证据 |
| --- | --- | --- | --- |
| UT-01 | 成员刷新结束后，旧选择流程不得重新提交已经删除的节点。典型反例：`members=[current]`，最终却选回 `removed`。成员版本检查与提交必须在能阻止刷新穿插的锁范围内完成。 | `protocol/group/urltest.go` 的 `membersVersion`、`UpdateOutbounds`、`performUpdateCheck`、`applySelectedUpdate` | `protocol/group/urltest_regression_test.go`：`TestURLTestRefreshCannotRecommitRemovedMember`；保留 `urltest_test.go` 中连接代际和取消相关测试。 |
| UT-02 | 嵌套组的网络能力由当前有效成员递归决定；只有 TCP 的内层组不能赢得 UDP 选择。内层成员刷新后，外层仍能改用可用 UDP 备用节点；递归检查必须防止循环。 | `protocol/group/network.go` 的 `supportsNetwork`；`urltest.go` 的选择与拨号；`selector.go` | `protocol/group/urltest_regression_test.go`：`TestURLTestNestedTCPOnlyGroupMustNotWinUDPSelection`。 |
| UT-03 | 测速结果、失败状态、在途预约和等待必须按节点与规范化后的测试 URL 隔离。不同 URL 不能共享一次成功并跳过另一目标；等价 URL 可以共用。嵌套 URLTest 使用自己的测试 URL，Selector 继承父级目标。 | `common/urltest/urltest.go` 的 `NormalizeURL`、作用域键与预约；`protocol/group/network.go` 的 `URLTestHistoryScope`；`daemon/url_test_scheduler.go` | `TestURLTestDistinctProbeURLsMustNotReuseOtherTargetSuccess`；`common/urltest/urltest_test.go` 的 `TestHistoryStorageIsolatesTargetsAndReservations`、`TestHistoryStorageEquivalentURLsShareScope`。 |
| UT-04 | 负数 `interval` 和 `idle_timeout` 返回配置错误，不得进入 `time.NewTicker` 后 panic；零值继续表示使用默认值。 | `protocol/group/urltest.go` 的 `NewURLTestGroup` | `protocol/group/urltest_regression_test.go`：`TestURLTestNegativeIntervalMustBeRejected`。当前测试仅覆盖负 interval；负 idle timeout 由同一构造函数条件拒绝，触碰此判断时应补充对应回归场景。 |
| UT-05 | 冷启动尚未正式选中时，连接链仍能按 TCP/UDP 显示实际使用的 fallback。读取 `Now()` / `NowForNetwork()` 或连接元数据不得提交选择、推进连接中断代际。 | `adapter/outbound_group.go`；`protocol/group/urltest.go` 的 `NowForNetwork`；`common/dialer/default.go`；`common/trafficcontrol/tracker.go` | `TestURLTestNowMustDescribeColdStartDialFallback`；`common/dialer/outbound_chain_test.go` 的 `TestOutboundChainUsesNetworkSpecificSelection`；`common/trafficcontrol/tracker_test.go` 的 `TestTrackerMetadataUsesNetworkSpecificSelection`。 |
| UT-06 | 延迟与容差相加必须使用不会发生 uint16 回绕的计算类型。当前 100 ms、候选 200 ms、容差 65400 ms 时，不能因溢出选中更慢节点。 | `protocol/group/urltest.go` 的 `selectFrom` | `protocol/group/urltest_regression_test.go`：`TestURLTestToleranceAdditionMustNotWrap`。 |
| UT-07 | 组测速必须等每个目标的新结果；完整快照中的旧成功不能提前结束本轮测速。序号与毫秒时间需要传到客户端，同一时间戳的不同结果仍可区分，旧结果不能覆盖较新结果。 | `adapter/experimental.go`；`common/urltest/urltest.go`；`daemon/started_service.proto`、`started_service.go`、`started_service_url_test_v2.go`；`experimental/libbox/command_types.go`、`manual_urltest.go`；SenVPN 的 `ProxyStateReducers.kt`、`UrlTestInterop.kt`、`OfflineUrlTestRunner.kt`、`ProxyModels.kt` | `TestHistoryStorageFailureAndRepeatedTimestampHaveDistinctSequences`、`TestHistoryStorageDoesNotReplaceCurrentWithOlderResult`；SenVPN `ProxyStateReducersTest` 的 `whole group waits for each new result even when full snapshots contain cached successes`。 |
| UT-08 | 失败与从未测试必须可区分。失败保留完成时间及结果序号，清除旧成功状态；旧快照不能把失败恢复为成功。单节点手动测速保持已有选择，这一行为不能被误当成待修 BUG。 | `common/urltest/urltest.go`；`daemon/started_service.go`；`experimental/clashapi/proxies.go`；`experimental/libbox/command_types.go`；SenVPN 的 `ProxyGroupMapper.kt`、`ProxyStateReducers.kt` | `daemon/url_test_regression_test.go` 的 `TestGroupSnapshotExposesLatestFailure`、`TestManualURLTestV2FailurePreservesSelection`；`TestHistoryStorageDoesNotRestoreOlderSuccessAfterNewerFailure`；SenVPN `ProxyGroupMapperTest`、`ProxyStateReducersTest` 的失败/旧快照及单节点选择测试。 |
| UT-09 | 在线完整运行时快照是成员列表的依据：订阅移除节点、组消失时应移除旧成员，同时保留配置中的组元数据。离线或单节点局部结果必须继续按局部更新合并，不能误删其他成员。 | 内核 `daemon/started_service.go` 的组快照、`protocol/group/selector.go` 与 `urltest.go` 的动态成员；SenVPN 的 `AndroidVpnRepository.kt`、`ProxyStateReducers.kt` | SenVPN `ProxyStateReducersTest` 的 `provider refresh removes vanished nodes and clears all nodes when group disappears`、`partial group update keeps missing result testing until it becomes timeout`、`runtime groups enrich configured groups without dropping empty groups`。 |

### 配套边界

- `daemon/started_service.proto` 的 `GroupItem.urlTestTimeMillis` / `urlTestSequence` 与 `URLTestItemV2Result.sequence` 及其生成代码、libbox 字段、SenVPN 映射必须一起核对。秒和毫秒不可混用；兼容旧字段的转换要保留。
- 成员刷新、测速去重、取消和连接代际的保护来自多次本地修复。不能只保留上表新测试而删掉 `protocol/group/urltest_test.go`、`selector_test.go` 中已有测试。
- 上游的 `References()`、选择变化通知、空闲连接管理应与本地修复同时保留。恢复本地旧文件不能连带删掉已经接受的上游功能。
- SenVPN 对应源代码在 `app/src/main/java/com/senvpn/android/`，测试在 `app/src/test/java/com/senvpn/android/`。核对界面结果时必须追踪内核字段到这些消费端，不能只看 Go 代码。

## 每次上游同步的检查流程

1. 检查工作区、当前分支和 remotes；记录本地 HEAD 与 fetch 前的 `upstream/testing`。保护无关的未提交改动。
2. Fetch 后先计算真实上游增量。普通历史可用 `git diff --name-status HEAD...upstream/testing`；历史重写时结合 `git log --cherry-mark --right-only HEAD...upstream/testing`、merge-base 和旧/新上游的 `git range-diff`。不要把所有右侧提交都当成新功能。
3. 对照下列观察范围及每条行为，检查上游是否改变实现、调用方、接口、依赖或测试。涉及文件没有冲突也要审查；观察范围只是入口，不是排除其他间接影响的白名单。
4. 对受影响条目标注 `保留本地`、`语义合并` 或 `上游等价/更好替代`。证据不足时保留本地行为；采纳新实现时保留或迁移原回归场景。
5. 根据影响运行下面的检查。失败先区分行为退化与环境问题，不通过删除断言、修改期望或停用测试来完成同步。
6. 在合并说明或同步报告中简要列出受影响编号、决策、测试及限制。实现入口变化或有新的 BUG 修复时，同步维护本文件；已替代条目记录替代提交与证据，不直接抹去背景。

### 必查范围

```text
protocol/group/                 common/urltest/
adapter/outbound_group.go      adapter/outbound.go
adapter/experimental.go        adapter/outbound/
common/dialer/                 common/trafficcontrol/
daemon/started_service*        daemon/url_test*
experimental/libbox/           experimental/clashapi/
proxyprovider/                 option/group.go
route/reference*               common/interrupt/
go.mod                         go.sum
```

依赖升级也要检查：`sing` 的并发/observable 工具、传输层复用与取消行为改变，可能在这些本地文件完全没有 diff 的情况下影响 URLTest。

## 验证命令与范围

仅修改本维护文件或 `AGENTS.md` 时，核对路径、测试名称、链接和 Git 忽略规则即可，无需重跑内核与 Android 构建。

同步触及上述实现或可能影响它们的依赖时，从本仓库根目录运行对应包测试；下列命令覆盖已登记的内核链路：

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'codex-sing-box-gocache'
go test ./protocol/group ./common/urltest ./daemon ./common/dialer ./common/trafficcontrol ./experimental/clashapi ./experimental/libbox ./proxyprovider -count=1
```

涉及成员版本、选择提交、历史预约或结果发布等并发行为时，另在可用的 race 环境执行：

```powershell
go test -race ./protocol/group ./common/urltest ./daemon -count=1
```

接口或依赖有变化时按实际影响扩展到主模块全量测试、`test/` 独立模块编译、Linux/Android 编译。修改 proto 时使用项目生成器重生成，不能手改 protobuf 描述符。

涉及 UT-07 至 UT-09 的跨端契约时，还要在兼容的新 AAR 下检查 SenVPN 的 `ProxyStateReducersTest`、`ProxyGroupMapperTest`、编译与相关调用方。不要把旧 AAR 上通过的测试当作新内核兼容证明；更新 SenVPN 的版本固定和发布按当次任务范围执行。

文档和指令负责提醒检查；回归测试负责验证已知场景。它们都不能替代语义审查，也不构成真机、路由器或生产网络验证。
