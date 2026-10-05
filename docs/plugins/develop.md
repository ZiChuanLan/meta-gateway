# 开发插件

插件是**独立进程**，用任意语言写。网关通过 HTTP 与它通信，不把插件代码链进主进程。

## 一、最小插件

一个插件就是一个 HTTP 服务，至少提供两个端点：

| 路径 | 作用 |
| :--- | :--- |
| `/plugin.json` | manifest |
| `/healthz` | 健康检查 |

网关注册时会：**拉 manifest → 健康检查 → 安装启用**。

## 二、manifest 里有什么

| 字段 | 作用 |
| :--- | :--- |
| `id` / `name` / `version` | 身份 |
| 页面路径 | 控制台用 iframe 内嵌展示 |
| `config_fields` | 声明配置项（见下） |
| `permissions` | 声明式权限，`resource:action` 格式 |
| `hooks` | 拦截钩子声明（见[拦截钩子](./hooks)） |

## 三、配置字段（`config_fields`）

值存在 `plugin_configs` 表，管理员保存后以 **base64 JSON 经 `X-Plugin-Config` 请求头**注入**每一次**反代请求。

所以插件不需要自己的配置存储——它每次请求都能拿到最新配置。

### 注册时的校验

| 规则 | 说明 |
| :--- | :--- |
| key 唯一 | 不能有两个同名字段 |
| 类型合法 | 只接受声明的类型 |
| `select` 必须有 options | 否则渲染不出选项 |
| `number` / `bool` 的默认值类型正确 | 否则前端会存进一个解不开的值 |

### 限制

| 项 | 值 |
| :--- | :--- |
| 形状 | JSON 对象 |
| 上限 | **4 KiB** |
| 响应缓存 | `no-store` |

## 四、权限

`permissions` 用 `resource:action` 格式，**注册时校验并去重**。

它是**声明性**的：网关检查声明的格式与去重，并据此决定是否允许某些能力（例如声明 `hooks` 必须同时声明 `relay:intercept`）。

> 权限不是沙箱。插件是独立进程，能做的事由你给它的运行环境决定——所以环境变量白名单才是真正的边界（见下）。

## 五、进程托管

托管形态下由网关拉起子进程，并注入运行时信息：

```text
META_GATEWAY_PLUGIN_{ID,DIR,ADDR,PORT,KEY}
```

| 变量 | 作用 |
| :--- | :--- |
| `META_GATEWAY_PLUGIN_ID` | 插件 id |
| `META_GATEWAY_PLUGIN_DIR` | 插件目录（持久数据放这里） |
| `META_GATEWAY_PLUGIN_ADDR` / `_PORT` | 监听地址与端口 |
| `META_GATEWAY_PLUGIN_KEY` | 网关调用你时带的密钥 |

插件目录会写入 `.meta-gateway.json` 持久化运行时 spec。

### 环境变量白名单

子进程只拿到：

- `PATH`、`HOME`、代理相关变量、`TZ`
- `META_GATEWAY_PLUGIN_*`

**`ADMIN_TOKEN` 与 `MASTER_KEY` 不传给插件。**

## 六、反代与鉴权

网关把 `/admin/plugins/{id}/proxy/*` 反代给你的服务，并：

- 自校验 `Authorization` 或 `?t=`（控制台用 iframe 内嵌时传 `?t=`）；
- 注入 `X-Plugin-Key`。

控制台在 `/console/plugins/:id` 用 iframe 内嵌，iframe 的 sandbox **保留脚本与表单能力，但阻断父源读取 sessionStorage 里的管理员 token**。

> 插件通信走**独立的 HTTP 客户端，绕过出网 SSRF 策略**。信任模型是「管理员主动安装」——所以不要安装来路不明的插件。

容器内注册宿主机上的插件时，用 `host.docker.internal` 作为地址。

## 七、示例与参考

官方插件源码在 [`ZiChuanLan/meta-gateway-plugins`](https://github.com/ZiChuanLan/meta-gateway-plugins)，每个插件独立 go module 与 CI：

| 插件 | 作用 |
| :--- | :--- |
| `demo-plugin` | **协议参考实现**——从它开始读最快 |
| `jev-router` | 一个可用的 `route` 钩子 |

## 八、写插件的几条建议

1. **`/healthz` 要便宜。** 网关会定期问它，慢的健康检查会拖慢注册与状态刷新。
2. **配置每次请求都读 `X-Plugin-Config`**，不要缓存到进程启动时——管理员改配置后网关立刻注入新值，缓存会让你看到旧配置。
3. **实现钩子时先判 `X-Meta-Hook-Origin` / `X-Meta-Hook-Depth`**，否则你的插件会在嵌套调用里拦截自己。
4. **别假设请求体是合法 JSON**。`response` 钩子拿到的是上游应答，可能不是 JSON；解析失败时应该「没有意见」，而不是报错。
5. **失败要快速返回**。超时在网关侧算「没有意见」，但慢的插件会拖慢每一个匹配的请求。

## 相关

- [插件 / 概览](/plugins/)
- [拦截钩子](./hooks)
- [安装与市场](./install)
