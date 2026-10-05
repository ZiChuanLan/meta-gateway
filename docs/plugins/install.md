# 安装与市场

## 一、两种形态

| 形态 | 说明 |
| :--- | :--- |
| **直连 sidecar** | 注册一个**已经在运行**的服务地址，网关反代到它 |
| **可下载安装包** | 从插件市场安装，网关下载、校验并托管进程 |

### 直连 sidecar

```
POST /admin/plugins/register   { "url": "http://host:port", "api_key": "..." }
```

网关注册时会**拉 manifest → 健康检查 → 安装启用**。

容器内注册宿主机上的插件，地址用 `host.docker.internal`。

### 可下载安装包

从市场安装时，注册表提供两种制品来源：

| 来源 | 说明 |
| :--- | :--- |
| `direct artifacts` | 注册表直接给下载 URL + 大小 + sha256 |
| `github-release` | 从 GitHub Release 取制品 |

安装包是一个 zip，内含 `plugin.json` 与入口可执行文件。

## 二、安装过程的校验与原子性

```
下载 → 校验大小 → 校验 sha256 → 解压到 <pluginDir>.staging
     → 原子替换（旧目录先改名为 backup）→ 成功则删 backup / 失败则回滚
```

| 步骤 | 失败后果 |
| :--- | :--- |
| 大小超限 | 拒绝，不落盘 |
| **sha256 不匹配** | 拒绝（`plugin_artifact_checksum_mismatch`），不替换 |
| 解压失败 | 拒绝，原目录不动 |
| 替换后启动失败 | **回滚**到 backup 目录 |

> **校验有两个来源**：注册表给的 checksum 与制品自带的 `artifact.SHA256`，两个都要过。

## 三、管理接口

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/plugins` | 已安装插件 |
| `GET /admin/plugins/status` | 运行状态 |
| `GET /admin/plugins/catalog` | 官方目录 |
| `GET /admin/plugins/market` | 市场列表 |
| `GET /admin/plugins/hooks` | 钩子声明（排查用） |
| `POST /admin/plugins/register` | 注册直连 sidecar |
| `PUT /admin/plugins/{id}` | 更新 sidecar 配置 |
| `DELETE /admin/plugins/{id}` | 卸载 |
| `POST /admin/plugins/{id}/install` | 安装 |
| `POST /admin/plugins/{id}/activate` | 激活 |
| `POST /admin/plugins/{id}/enable` / `disable` | 启用 / 停用 |
| `POST /admin/plugins/market/{id}/install` | 从市场安装（可指定版本） |
| `GET` / `PUT /admin/plugins/{id}/config` | 插件配置 |

## 四、自建注册表

内置的官方注册表**总是**可用；额外的注册表用：

```dotenv
PLUGIN_MARKET_URLS=https://my-registry.example/index.json
```

（逗号分隔，追加而不替换内置的。）

相关变量：

| 变量 | 作用 |
| :--- | :--- |
| `PLUGINS_DIR` | 插件安装目录 |
| `PLUGIN_CATALOG_URL` | 官方目录地址 |
| `PLUGIN_MARKET_URLS` | 额外注册表 |

## 五、生态现状

| 位置 | 内容 |
| :--- | :--- |
| 本仓库 | 只保留**协议**（`internal/plugins`、`internal/proxy/hooks.go`） |
| [`ZiChuanLan/meta-gateway-plugins`](https://github.com/ZiChuanLan/meta-gateway-plugins) | 官方注册表 + 一方插件源码（各自独立 go module 与 CI） |

第三方插件在自己的仓库里，通过一条注册表条目（或用 `PLUGIN_MARKET_URLS` 指向自建注册表）接入——**所以没有任何插件源码需要合并到本仓库**。

## 六、拓展页只做三件事

1. 注册 sidecar；
2. 浏览插件市场；
3. 管理已安装插件。

> **签到与交换已经是内置功能，不是可开关的扩展。** 别去找「扩展开关」——启动引导会撤销它。

## 相关

- [插件 / 概览](/plugins/)
- [拦截钩子](./hooks)
- [开发插件](./develop)
