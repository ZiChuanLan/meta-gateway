# meta-gateway 代码质量审查报告

> 第一轮完成,第二轮追加见文末。
> 审查范围:Go 1.26.4,internal/ 下 27+ 包,约 25K 行(非测试)。全部结论来自实际读码 + grep 证据。

---

## 0. 验证基线

- `go version`: go1.26.4 windows/amd64
- `go build ./...`:**通过,无错误**
- `go vet ./...`:**通过,无输出**
- 第一轮已完整阅读包:`proxy`、`relay`、`routing`、`adapters`(全部 11 个文件)、`account`、`checkin`、`discovery`、`healthsweep`、`alert`、`auth`、`crypto`、`backup`、`usage`、`ratelimit`、`outbound`、`domain`、`exchange/parse.go`、`store`(store.go/time.go/channel.go)、`httpapi/relay.go`
- 第一轮未读(第二轮覆盖):`httpapi/admin.go`、`store/exchange.go`、`store/route.go`、`config`、`runtimeconfig`、`plugins`、`webdavsync`、`observability`、`sitedetect`、`webhook/notifier.go`、`webui`、`cmd/server/main.go`、`exchange/service.go`、`exchange/format.go`

---

## 一、死代码(全部附 grep 证据)

| # | 位置 | 问题 | 证据 | 严重度 |
|---|---|---|---|---|
| 1 | `internal/proxy/proxy.go:1111` | `func classify(result *relay.Result) (string, bool)` 全仓库 0 调用 | `grep -rn "classify(" --include="*.go" .` 仅 1 条命中 = 定义本身;生产中用的是 `classifyForChannel` | **P1** |
| 2 | `internal/proxy/proxy.go:786` | `(s *Service) resolveAPIKey(channel)` 全仓库 0 调用(测试也无) | `grep -rn "resolveAPIKey"` 命中仅定义行 + `resolveAPIKeyPool`(被 discovery.go:133、proxy.go:502/1088 使用,是另一个函数) | **P1** |
| 3 | `internal/proxy/proxy.go:352` | `(s *Service) Forward(...)` 0 调用(测试也没有) | `grep -rn "\.Forward("` 无命中(除 `ForwardWithMeta/ForwardContext/ForwardWithHeaders`) | **P1** |
| 4 | `internal/proxy/proxy.go:345` | `(s *Service) ChatCompletions(...)` 仅测试调用 | 命中:proxy_test.go:119/152/204、circuit_breaker_test.go:234、proxy_sticky_test.go:145/159/184/194/210/212 | P2(测试专用) |
| 5 | `internal/relay/relay.go:142` | `DecodeJSONRequestBody` 全仓库 0 调用 | `grep -rn "DecodeJSONRequestBody"` 仅定义行 | **P1** |
| 6 | `internal/relay/relay.go` ~110/53 | `(r *Relay) Models()` 与 `ChatCompletions()`(非 Context 版)仅测试使用 | `.Models(` 命中仅 relay_test.go:127/156/160;`ChatCompletions(` 非 Context 命中仅 relay_test.go:58/88 | P2 |
| 7 | `internal/adapters/site_profile.go:35` | `type siteProfileEntry struct` 声明后从未使用(`siteProfiles` 是 `map[string]SiteProfile`) | `grep -rn "siteProfileEntry"` 仅定义行 | **P1** |
| 8 | `internal/usage/usage.go:279` | `EstimateCost` 仅测试使用 | `grep` 命中仅 usage_test.go:142/143 | P2 |
| 9 | `internal/adapters/openai.go:34` | `Error.RetryAfter` 字段**全生产代码从不赋值**,却被子系统读取 → 功能静默失效 | 写入方 0 处;读取方 `account/service.go:1260 retryAfterFrom`;赋值仅测试 service_test.go:1014。即"429 时按上游 Retry-After 延长暂停"的逻辑永远走默认 60s 分支 | **P1(功能死代码)** |
| 10 | `internal/exchange/parse.go` `normalizeType` 内 | `_ = adapters.CanonicalType(value)` 结果丢弃,残留语句 | 直接读码可见,无任何副作用 | P2 |
| 11 | `internal/adapters/adapters.go:33` | 注释引用 `FamilyOf` 函数,该函数不存在(全仓库 grep 无此符号) | `grep -rn "FamilyOf"` 仅注释命中 | P2 |
| 12 | `internal/httpapi/relay.go:29-36` | `RelayProxy` 接口含 `Forward`/`ChatCompletions` 两个生产从不调用的方法,逼着 mock 和 Service 实现死方法 | 生产调用点全用 `ForwardWithMeta`/`ChatCompletionsWithMeta` | P2 |
| 13 | `internal/relay/relay.go:53-58` | `ChatCompletionsContext` 的 `stream` 参数完全未用(直接转 `ForwardContext`,POST 恒等) | 读码可见 | P2 |
| 14 | `internal/httpapi/relay.go:521` | `writeUpstreamResult` 的 `clientFamily` 形参在整个函数体内从未使用(调用方 2 处传值) | 读码可见 | **P1(死参数)** |

## 二、垃圾代码

| # | 位置 | 问题 | 严重度 |
|---|---|---|---|
| 15 | `internal/backup/service.go` `Create()` | 7 个失败分支全部 `return nil, errors.New("backup failed")`,**吞掉全部底层错误**(onlineCopy/Verify/rename 的真实原因全部丢失,运维无法定位) | **P1** |
| 16 | `internal/account/service.go` | 文件混合行尾:1313 行 CRLF + 76 行 LF(`file` 报告 "with CRLF, LF line terminators"),checkin/service.go 同样混合 | P2 |
| 17 | `internal/account/service.go:31-35` | `Error.Error()` 只输出 `Category`,`Kind` 字段在错误信息中完全被忽略 | P2 |
| 18 | `internal/adapters/anthropic_downstream.go:134` | `func errorsNew(text string) error { return fmt.Errorf("%s", text) }` 是 `errors.New` 的劣化重写,仅 1 处调用 | P2 |
| 19 | `internal/proxy/proxy.go:782` 附近 | `resolveUpstreamURL` 两个失败分支都返回硬编码 `"proxy: invalid base url"`,把 `adapter.BuildUpstreamURL` 的具体错误丢弃 | P2 |
| 20 | 魔数堆积 | `proxy.go` `recordKeyFailure` 硬编码阈值 `5`、禁用期 `30 * time.Minute`;`account/service.go`/`discovery/discovery.go` 各有一份 `1200 * time.Millisecond` 重试睡(重复魔数);`account/service.go` financeForChannel 硬编码 `quotaPerUnit = 500000` 兜底;`proxy.go` 非流式读上限 `8<<20`、`httpapi/relay.go` 请求体 `10*1024*1024` 散落各处 | P2 |
| 21 | `internal/healthsweep/service.go` `probeOnce` | 与 `discovery.Probe` **双重写库**:discovery.Probe 成功时已 `RecordProbeSuccess`,healthsweep 又写一次(每次探测多一次 UPDATE) | P2 |
| 22 | `internal/alert/alert.go` `DailySummary.runOnce` | 降级且有失败数的渠道同时计入 `degraded` 和 `unhealthy` 两个桶,摘要数字口径混乱(注释自相矛盾) | P2 |
| 23 | `internal/routing/routing.go` `pickLatencyAware` | 缩进断裂(`score := weight` 块缩进错误),可读性差 | P3 |
| 24 | `internal/proxy/proxy.go:808` | `resolveUpstreamURL` 中 `upstreamURL` 构造失败与 baseURL 缺失共用同一错误串,错误分类无法区分 | P2 |

## 三、重复代码(文件:行 + 对比)

| # | 重复内容 | 位置对比 | 严重度 |
|---|---|---|---|
| 25 | `firstNonEmpty` 完全相同的函数 **3 份** | `internal/account/service.go:1348`、`internal/adapters/anthropic.go:66`、`internal/config/config.go:477`(签名还不同:前两者 variadic string,config 版接收 `[]string`) | P1 |
| 26 | `platformUserID(raw)` 两份(仅 checkin 版多了 `ensureJSONEOF`) | `internal/account/service.go:~1327` vs `internal/checkin/service.go:~436` | P1 |
| 27 | `persistPlatformUserID` 两份,函数体逐行相同 | `internal/account/service.go:~1285` vs `internal/checkin/service.go:~480` | P1 |
| 28 | `zero([]byte)` 两份 | `internal/account/service.go:1375` vs `internal/checkin/service.go:457` | P2 |
| 29 | `ErrorKind`+`Error{Kind,Category}`+`Error() string{fmt.Sprintf("… failed: %s", Category)}` 模式 **4 份** | account/service.go:22、checkin/service.go:~28、discovery/discovery.go:~25、adapters(openai.go ErrorKind + checkin.go CheckinError) | P2 |
| 30 | Sweep 与 DailySummary 的 Start/SetInterval/Stop/newTicker/tick 循环脚手架 ~60 行逐行重复 | `internal/alert/alert.go`(两个 struct) | P2 |
| 31 | `pickLatencyAware`/`pickErrorAware`/`pickWeighted` 三个加权抽签函数结构几乎相同;`scoreFor` 与 `pick` 内 latency/error 模式 switch 重复 2 次 | `internal/routing/routing.go` | P2 |
| 32 | "content 字符串或 parts 数组 → 纯文本"扁平化逻辑 3 份 | `adapters/anthropic.go` `messageContentText`、`adapters/anthropic_downstream.go` `anthropicPartsToOpenAIContent`、`adapters/gemini.go` `contentToText` | P2 |
| 33 | "校验 base URL + 拼接路径" 4 个近似实现(校验条件逐字相同:IsAbs/Host/User/Scheme) | `adapters/openai.go` JoinOpenAIPath、`adapters/anthropic.go` JoinAnthropicPath、`adapters/account.go` accountEndpoint、`adapters/gemini.go` parseGeminiBaseURL | P2 |
| 34 | `forwardPassthrough` 与 `forwardModelRequest` 后半段(proxyReq 构建 + writeUpstreamResult + RecordUsage + UpdateMetaByRequestID)复制粘贴 | `internal/httpapi/relay.go`(两个 handler) | P1 |

## 四、统计与 Top 10(第一轮)

**统计**:已确认问题点 **34 个** —— 死代码 14(P1×7:1,2,3,5,7,9,14;P2×7)、垃圾代码 10(P1×1:15;P2×8;P3×1)、重复代码 10(P1×4:25,26,27,34;P2×6)。**P0 = 0**(build/vet 全绿,无编译级问题);**P1 = 12;P2 = 21;P3 = 1**。

**按严重度 Top 10**:
1. `proxy/proxy.go:1111` `classify` 死函数(P1)
2. `proxy/proxy.go:786` `resolveAPIKey` 死函数(P1)
3. `adapters/openai.go:34` `Error.RetryAfter` 永不赋值 → 429 Retry-After 暂停功能静默失效(P1)
4. `backup/service.go` `Create()` 7 处 `errors.New("backup failed")` 吞掉根因(P1)
5. `relay/relay.go:142` `DecodeJSONRequestBody` 死函数(P1)
6. `httpapi/relay.go:521` `writeUpstreamResult` 的 `clientFamily` 死参数(P1)
7. `firstNonEmpty` 三份拷贝 + `platformUserID`/`persistPlatformUserID` 跨 account/checkin 双份(P1)
8. `httpapi/relay.go` `forwardPassthrough`/`forwardModelRequest` 大段复制粘贴(P1)
9. `routing/routing.go` 三个 pick 函数近重复 + 缩进损坏(P2)
10. `alert/alert.go` Sweep/DailySummary 脚手架重复 + 降级/不健康双重计数(P2)

**修复建议(摘要)**:死函数直接删除(classify、resolveAPIKey、Service.Forward、DecodeJSONRequestBody、siteProfileEntry);RetryAfter 要么在 `doJSON`/checkin 适配器 429 分支赋值,要么删字段+删 `retryAfterFrom`;backup 改 `fmt.Errorf("backup failed: %w", err)`;firstNonEmpty/platformUserID/persistPlatformUserID/zero 收敛到共享小包(如 internal/xutil);relay.go 两个 handler 提取公共 `forwardBody` 函数;alert 两个循环器抽公共基类;routing pick 三合一(传入 latency/error provider 是否启用的参数)。

---

## 第二轮追加

> 第二轮覆盖:`httpapi/admin.go`(全读)、`httpapi/router.go`/`json.go`/`try.go`/`models_cache.go`/`grouplimit.go`/`lifecycle.go`(全读)、`store/route.go`/`exchange.go`/`group.go`/`proxylog.go`/`runtime_settings.go`(全读)、`config/config.go`(全读)、`runtimeconfig`(全读)、`plugins/service.go`(全读)、`webdavsync`(service/client/settings/errors 全读)、`observability`(全读)、`sitedetect`(全读)、`webhook/notifier.go`(全读)、`exchange/service.go`+`format.go`(全读)、`webui/embed.go`(全读)、`cmd/server/main.go`(全读)。未逐行通读(仅 grep 验证符号使用):`httpapi/account.go`/`operations.go`/`audit.go`/`checkin.go`/`discovery.go`/`exchange.go`/`plugins.go`/`webdav.go`/`runtime_settings.go`/`landing.go`/`clientip.go`、`store/usage.go`/`audit.go`/`backup.go`/`checkin.go`/`credential.go`/`discovery.go`/`downstream_key.go`/`plugin.go`/`site.go`/`migrations.go`、`webdavsync/decrypt.go`/`path.go`/`scheduler.go`、`tools`、`cmd/e2e-*`。

### 二轮验证基线

- `go build ./...`、`go vet ./...` 依旧全绿。
- 新增证据:`gofmt -l internal cmd tools` 列出 **24 个未格式化文件**:internal\account\service.go、internal\adapters\account.go、internal\checkin\service.go、internal\config\config.go、internal\domain\models.go、internal\healthsweep\service.go、internal\httpapi\admin.go、internal\httpapi\relay_test.go、internal\httpapi\router.go、internal\outbound\policy.go、internal\outbound\policy_test.go、internal\proxy\circuit_breaker.go、internal\proxy\circuit_breaker_test.go、internal\routing\routing.go、internal\routing\routing_test.go、internal\runtimeconfig\runtimeconfig.go、internal\store\channel.go、internal\store\group.go、internal\store\proxylog.go、internal\store\route.go、internal\store\runtime_settings.go、internal\store\store_test.go、internal\webhook\notifier.go、tools\main.go。

### 二轮死代码(grep 证据)

| # | 位置 | 问题 | 证据 | 严重度 |
|---|---|---|---|---|
| 35 | `internal/store/route.go` `RouteMemberStore.FailureCount` | 全仓库 0 调用 | `grep -rn "\.FailureCount("` 无命中 | P2 |
| 36 | `internal/store/route.go` `RouteStore.GetByModel` | 0 调用;其 exact→wildcard 逻辑与 `RoutingCandidates` 内部重复 | `grep -rn "\.GetByModel("` 无命中 | P2 |
| 37 | `internal/store/route.go:587` `DeleteByRoute` | 0 调用 | grep 仅定义行 | P2 |
| 38 | `internal/store/proxylog.go:229` `UpdateTokensByRequestID` | 生产 0 调用,仅 store_test.go:970 使用(tokens 现已在 Insert 时落库,此为遗留路径) | grep 命中仅定义+测试 | P2 |
| 39 | `internal/store/proxylog.go` `ProxyLogStore.List`(无过滤版) | 仅测试使用(store_test.go:850/973/1193),生产全走 `ListFilter` | grep 证据 | P2 |
| 40 | `internal/webdavsync/client.go` `Client.Probe` | 0 调用(含测试) | `grep -rn "\.Probe("`(排除 account/discovery/health)无命中 | P1 |
| 41 | `internal/httpapi/router.go` `New()`(无依赖构造器) | 仅测试使用(auto_disable_test.go:35、checkin_integration_test.go:38、gemini_relay_test.go:70),生产用 `NewWithDependencies` | grep 证据 | P2 |

### 二轮垃圾代码

| # | 位置 | 问题 | 严重度 |
|---|---|---|---|
| 42 | `gofmt -l` 24 个文件 | 全仓库约 1/4 的 Go 文件未过 gofmt(含 config.go、admin.go、router.go、runtimeconfig.go、route.go 等核心文件),混排缩进/对齐损坏 | P2 |
| 43 | `internal/config/config.go` Load() | `relayRate, err := envInt("RELAY_RATE_PER_MINUTE",…)` 的错误检查与调用脱节:错误被约 20 行后 `stableFirstPromote` 之后的孤立 `if err != nil { return nil, err }` 兜住,依赖变量活性,中间插入代码即失效 | P2 |
| 44 | `internal/config/config.go` Config 结构体 | 字段注释/缩进混乱(`WebhookURL` 块顶格、`RelayRatePerMinute` 与 `RelayRateBurst` 同行),`CheckinEnabled bool` 对齐损坏 | P2 |
| 45 | `internal/store/runtime_settings.go` `Save()` | `latencyState` 把 0 和 -1 都映射成 1("default on"),而 -1 的语义是"未覆盖"(见 `rowToEditableWithEnv`);保存未覆盖行会把 -1 静默写成 1,与读取端语义冲突 | **P1(潜在静默改配置)** |
| 46 | `internal/httpapi/router.go` | `runtimeController.Bootstrap()` 只在 `if runtimeController == nil` 的 fallback 分支内调用;若依赖方传入自建 Controller(测试/未来嵌入),Bootstrap 永不执行 | P2 |
| 47 | `internal/httpapi/admin.go` `updateSite`/`createCredential` 等 | `updated, _ := h.db.Site.GetByID(id)` 忽略错误与 nil,GetByID 失败时向客户端写出 `null` 200 响应 | P2 |
| 48 | `internal/plugins/service.go` `EnsureOfficialModulesInstalled` | 第一次 `installed`/`have` 循环是死工作:紧接着重新 `List()` 覆盖;`operations` 清理逻辑可合并到一次循环 | P2 |
| 49 | `internal/httpapi/admin.go` `createDownstreamKey` | 匿名请求结构体缩进错乱(`Name` 字段 1 tab,其余 2 tab) | P3 |
| 50 | `internal/store/group.go` `AddUsage` | `func (s *GroupStore) AddUsage(...) error {	name = …` 函数体首行与花括号同行,格式损坏;`invalidateCache` 与 Upsert/Delete/AddUsage 内联 `delete(cache)` 三处重复实现 | P3 |
| 51 | `internal/checkin/scheduler.go` | `cron.NewParser(cron.Minute|…)` 在 `NewScheduler` 与 `SetSchedule` 各构造一次,可提为包级工厂 | P3 |
| 52 | `internal/runtimeconfig/runtimeconfig.go` `New()` | `env := Editable{` 缩进错乱;`Editable` 结构体字段对齐损坏(gofmt 列表已含) | P3 |

### 二轮重复代码

| # | 重复内容 | 位置对比 | 严重度 |
|---|---|---|---|
| 53 | `boolInt` 完全相同 3 份 | `internal/store/route.go:595`、`internal/runtimeconfig/runtimeconfig.go:395`、`internal/observability/metrics.go:81` | P2 |
| 54 | `sortedKeys`(map→排序 key 列表)2 份 | `internal/adapters/anthropic.go:336`、`internal/observability/metrics.go:101` | P3 |
| 55 | 明文清零 helper 4 份 | `account/service.go:1375 zero`、`checkin/service.go:457 zero`、`exchange/service.go clearBytes`、`discovery/probeModels` 内联循环 | P2 |
| 56 | **~60 行 SQL+Scan 整块复制**:`listCandidatesByRoute` 与 `RoutingCandidates` 的 SELECT(25 行)+ Scan(30 行)逐字相同,仅后者多一个 `rate_limited_until` WHERE 条件;`RouteStore.GetByModel` 又重复 exact→wildcard 逻辑 | `internal/store/route.go`(两处) | **P1(改一处漏一处的风险已存在:两查询已出现列顺序/格式漂移)** |
| 57 | worker-pool 模板复制:`syncKeysAfterImport` 与 `discoverAfterImport`(jobs+out channel+N worker+汇总+sort)约 50 行结构相同 | `internal/exchange/service.go` | P2 |
| 58 | `platformUserIDFromMeta` 第 3 份 platform_user_id 提取(且是手写字符串扫描,与前两份 JSON 解码实现不同) | `internal/store/exchange.go` vs `account/service.go`、`checkin/service.go` | P2 |
| 59 | `resolveAPIKeyPool` 两份同语义实现(绑定 key 优先+站点池+去重;一个返回明文 []string,一个返回 []domain.Credential) | `internal/proxy/proxy.go:731` vs `internal/discovery/discovery.go:259` | P2 |
| 60 | `safeKey`/`createKeyResponse`/`createDownstreamKey` 匿名结构 3 个 13+ 字段响应结构重复(字段名几乎一致,`CreatedAt` 格式化字符串 `"2006-01-02T15:04:05Z07:00"` 在 admin.go 出现 3 次) | `internal/httpapi/admin.go` | P2 |
| 61 | `Editable↔RuntimeSettingsRow` 双向字段映射 ~60 行逐字段重复(New 里 env 初始化第 3 遍) | `internal/runtimeconfig/runtimeconfig.go`(rowToEditable / Update / New) | P2 |

### 二轮修复建议

- 删除死符号:FailureCount、GetByModel、DeleteByRoute、UpdateTokensByRequestID、Client.Probe(保留 List 给测试或删除测试改用 ListFilter)。
- route.go:`RoutingCandidates` 复用 `listCandidatesByRoute` + 追加 rate_limited 过滤参数;`GetByModel` 删除。
- 全局执行 `gofmt -w`,并在 CI 加 `gofmt -l` 检查。
- runtime_settings.Save:latencyState 仅在 -1 时取 env 默认,0 保持 0;或改用三态枚举。
- router.go:把 `Bootstrap()` 移到 `if` 之外,任何来源的 Controller 都执行。
- 抽公共包(如 `internal/xutil`):boolInt、sortedKeys、zeroBytes、firstNonEmpty、platformUserIDFromMeta。
- admin.go:合并 safeKey/createKeyResponse 为一个带 `omitempty` 的结构,时间格式化提为常量。
- config.Load:`RELAY_RATE_PER_MINUTE` 错误立即检查,删除 20 行后的孤立 `if err != nil`。

### 二轮统计(累计)

- 二轮新增 **27 个**问题点(死代码 7、垃圾 11、重复 9)。
- **累计 61 个**问题点:死代码 21、垃圾代码 21、重复代码 19。**P1 = 15**(第一轮 12:1,2,3,5,7,9,14,15,25,26,27,34 + 二轮 3:40,45,56),**P2 = 40**,**P3 = 6**。P0 = 0(build/vet 均绿;gofmt 不阻断编译,但 24 个文件未格式化是 CI 应修项)。
- 二轮 Top 5(按严重度):① 56 route.go 双份 60 行 SQL/Scan(P1);② 45 runtime_settings.Save -1→1 静默改配置(P1);③ 40 webdavsync.Client.Probe 死代码(P1);④ 42 全仓库 24 文件未 gofmt(P2 面广);⑤ 43 config.Load 错误检查脱节(P2)。
