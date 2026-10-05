# 插件

插件是**独立进程**，用任意语言写。网关通过 HTTP 与它通信，不把插件代码链进主进程。

## 两种形态

| 形态 | 说明 |
| --- | --- |
| **直连 sidecar** | 注册一个已经在运行的服务地址（`url`），网关反代到它 |
| **可下载安装包** | 从插件市场安装：注册表提供 `direct artifacts` 或 `github-release`，下载 zip（含 `plugin.json` 与可执行文件），校验 sha256 与大小，暂存 + 备份 + 原子替换，失败可回滚 |

托管形态下由网关拉起子进程，并把运行时信息通过环境变量注入：

```
META_GATEWAY_PLUGIN_{ID,DIR,ADDR,PORT,KEY}
```

插件目录会写入 `.meta-gateway.json` 持久化运行时 spec。

## 插件协议 v1

一个插件是一个 HTTP 服务，至少提供：

| 路径 | 作用 |
| --- | --- |
| `/plugin.json` | manifest：id、名称、版本、页面、配置字段、权限声明 |
| `/healthz` | 健康检查 |
| 页面路径 | 控制台用 iframe 内嵌展示 |

网关侧：

- 注册走 `POST /admin/plugins/register`（拉 manifest → 健康检查 → 安装启用）；
- 反代走 `/admin/plugins/{id}/proxy/*`，网关自校验 Authorization 或 `?t=`，并注入 `X-Plugin-Key`；
- 控制台在 `/console/plugins/:id` 用 iframe 内嵌，通过 `?t=` 传 token。

容器内注册宿主机插件用 `host.docker.internal`。

## 安全边界

| 机制 | 作用 |
| --- | --- |
| **环境变量白名单** | 子进程只拿到 `PATH` / `HOME` / 代理 / `TZ` + `META_GATEWAY_PLUGIN_*`；**`ADMIN_TOKEN` 与 `MASTER_KEY` 不传** |
| **iframe sandbox** | 保留脚本与表单能力，阻断父源读取 sessionStorage 里的管理员 token |
| **独立 HTTP 客户端** | 插件通信绕过出网 SSRF 策略——信任模型是「管理员主动安装」 |
| **声明式权限** | `permissions` 用 `resource:action` 格式，注册时校验并去重 |

## 插件配置

manifest 用 `config_fields` 声明配置项，值存在 `plugin_configs` 表，管理员保存后以 base64 JSON 经 `X-Plugin-Config` 请求头注入**每一次**反代请求。

限制：JSON 对象、上限 4 KiB、响应 `no-store`。注册时校验 `config_fields` 与 `permissions`：key 唯一、类型合法、`select` 必须有 options、`number` / `bool` 默认值类型正确。

## 拦截钩子

插件可以声明钩子，在三个环节介入转发：

1. **路由改选** —— 影响最终候选渠道；
2. **上游请求改写**；
3. **响应处理**。

钩子决策会记录在响应头里，便于排查「这次为什么走了这个渠道」。

## 生态现状

官方插件源码与注册表在 [`ZiChuanLan/meta-gateway-plugins`](https://github.com/ZiChuanLan/meta-gateway-plugins)（每个插件独立 go module 与 CI）。主仓只保留协议实现（`internal/plugins`、`internal/proxy/hooks.go`）。

> [!NOTE]
> **签到与交换已经是内置功能，不是可开关的扩展。** 拓展页只做三件事：注册 sidecar、浏览插件市场、管理已安装插件。
