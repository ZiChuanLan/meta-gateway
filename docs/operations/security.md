# 安全

## 一、管理面鉴权

| 变量 | 作用 |
| :--- | :--- |
| `ADMIN_TOKEN` | 部署管理员口令 |
| `ADMIN_TOKENS` | 额外接受的轮换口令（逗号分隔） |
| `ADMIN_USERNAME` | 管理员用户名（默认 `admin`）；可在控制台验证口令 + TOTP 后改，**改后优先于环境变量** |

### 会话令牌怎么存放

```
POST /admin/session   →  { "token": "<管理口令>" }  →  返回 session_token
```

之后所有管理接口用 `Authorization: Bearer <session_token>`。

**session token 默认只驻留内存**（操作员可以主动选择标签页级 `sessionStorage`）。它**不进 Cookie、不进 `localStorage`、不进 URL、不进配置、不进日志**。收到 `401` 即失效当前 UI 会话。

> 这条约束决定了几个连带设计：控制台不通过 URL 传 token；插件 iframe 用 `?t=` 传的是**插件自己的**令牌，且 iframe 的 sandbox 阻断父源读取 sessionStorage。

### 二次验证（TOTP）

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/totp/status` | 当前是否启用 |
| `POST /admin/totp/setup` | 生成密钥与二维码 |
| `POST /admin/totp/enable` | 校验一次后启用 |
| `POST /admin/totp/disable` | 关闭 |

### 管理面限流

| 变量 | 默认 |
| :--- | :--- |
| `ADMIN_RATE_PER_MINUTE` | 300 |
| `ADMIN_RATE_BURST` | 50 |

被限流拒绝的管理面请求**会产生审计事件**。

### 请求体上限

`MAX_ADMIN_BODY_BYTES` 限制管理面 JSON 请求体。中继端点有自己的按端点上限（图像 20/30 MB、音频 10/30 MB）。

## 二、下游面

| 变量 | 作用 |
| :--- | :--- |
| `CORS_ALLOWED_ORIGINS` | 放开 `/v1` 给浏览器调用。**留空 = 任意来源（`*`）**，这是零配置默认值；填值时为精确来源或 `*.example.com` 子域模式 |
| `TRUSTED_PROXY_CIDRS` | 只有来自这些网段的转发地址才被当作客户端 IP |

> [!WARNING]
> **`TRUSTED_PROXY_CIDRS` 留空时，CDN 的 IP 会糊在一起。** 所有请求看起来来自同一个来源，限流会互相影响。部署在 CDN / 反代之后必须设置它。

下游令牌按**已认证的令牌**隔离限流；管理面用另一个全局限流器。

## 三、凭据保护

| 机制 | 说明 |
| :--- | :--- |
| **AES-GCM 加密入库** | 上游密钥、Cookie、token 都以 `MASTER_KEY` 加密存储，只在出站转发前在内存中瞬时解密 |
| **下游令牌存哈希** | 明文只在创建时显示一次 |
| **零信任日志** | 日志/审计/错误响应体不含凭据；上游密钥只以 sha256 指纹出现 |
| **reveal 有审计** | 每次查看或轮换凭据都写审计事件 |
| **导出不含预览** | 带密文的导出不提供浏览器预览 |

`MASTER_KEY` 必须是 **≥ 32 字符**且**随数据库一起保存**。丢了它，所有加密字段永久不可解。

## 四、出网策略

**私网、环回、link-local、元数据及其他特殊上游地址默认拒绝。** 可信内网服务要按最窄范围开例外：

```dotenv
OUTBOUND_ALLOW_HOSTS=llm.internal.example
OUTBOUND_ALLOW_CIDRS=10.24.8.15/32
```

- DNS 在**每次连接**时校验，重定向会重新检查；
- 主机例外是**精确匹配**，不含子域名；
- **环境变量代理被禁用**——代理侧 DNS 会绕过上述保证。要用代理请在运行设置里配全局代理，或用渠道级 `proxy_url`。

## 五、`/metrics` 的独立凭据

```bash
curl -H "Authorization: Bearer $METRICS_TOKEN" http://127.0.0.1:4100/metrics
```

只有显式配置的 `TRUSTED_SCRAPER_CIDRS` 可以免 token 抓取。

> **不要把 `ADMIN_TOKEN` 复用成 `METRICS_TOKEN`。** 指标端点的暴露面与后果和管理面完全不同。

## 六、插件边界

| 机制 | 作用 |
| :--- | :--- |
| **环境变量白名单** | 插件子进程只拿到 `PATH` / `HOME` / 代理 / `TZ` + `META_GATEWAY_PLUGIN_*`；**`ADMIN_TOKEN` 与 `MASTER_KEY` 不传** |
| **iframe sandbox** | 保留脚本与表单，阻断父源读取 sessionStorage |
| **声明式权限** | `permissions` 用 `resource:action`，注册时校验去重 |
| **拦截需显式声明** | 声明 `hooks` 的插件必须同时声明 `relay:intercept`，否则注册被拒 |
| **独立 HTTP 客户端** | 插件通信绕过出网 SSRF 策略——信任模型是「管理员主动安装」 |

> 拦截会把匹配到的提示词与应答暴露给插件进程，所以它需要显式权限而不是默认开启。

## 七、第三方登录

OAuth 的 state + PKCE 存在**签名 cookie** 里（HMAC 用 `MASTER_KEY` 派生），不依赖内存也不依赖会话；回调要求 state 匹配。

两个容易误配的点：

1. **端点会分裂成两个视角**：`authorize_url` 由浏览器访问，`token_url` / `userinfo_url` 由网关访问。
2. **OAuth 客户端显式忽略容器环境代理**（`HTTP_PROXY` 常指向宿主机，用它访问 GitHub 会 `connection refused`）。

## 八、安全检查清单

- [ ] `ADMIN_TOKEN`、`MASTER_KEY`、`METRICS_TOKEN` 三者互不相同且都是随机值
- [ ] 部署在 CDN / 反代之后时设了 `TRUSTED_PROXY_CIDRS`
- [ ] 开启了 TOTP
- [ ] `MASTER_KEY` 已随备份单独保存
- [ ] 管理面**不直接暴露在公网**（走反代 + 访问控制，或只开在内网）
- [ ] `CORS_ALLOWED_ORIGINS` 按需收窄，而不是默认的 `*`
- [ ] 确认过插件都是自己装的

## 相关

- [出网策略](/upstream/#出网策略)
- [审计与日志](./audit-and-logs)
- [团队与用户](/team/)
- [参考 / 环境变量](/reference/env-vars)
