import {
	createContext,
	useCallback,
	useContext,
	useEffect,
	useMemo,
	useState,
	type ReactNode,
} from "react";

export type Locale = "en" | "zh-CN";

const LOCALE_KEY = "meta-gateway.locale";

type Dict = Record<string, string>;

const en: Dict = {
	"lang.en": "English",
	"lang.zh": "中文",
	"lang.switch": "Language",

	"common.cancel": "Cancel",
	"common.save": "Save",
	"common.create": "Create",
	"common.delete": "Delete",
	"common.edit": "Edit",
	"common.retry": "Retry",
	"common.close": "Close",
	"common.loading": "Loading",
	"common.empty": "Nothing here yet.",
	"common.error": "Something went wrong",
	"common.working": "Working...",
	"common.none": "None",
	"common.select": "Select",
	"common.actions": "Actions",
	"common.status": "Status",
	"common.name": "Name",
	"common.time": "Time",
	"common.latency": "Latency",
	"common.source": "Source",
	"common.category": "Category",
	"common.reward": "Reward",
	"common.enabled": "Enabled",
	"common.disabled": "Disabled",
	"common.ready": "Ready",
	"common.unavailable": "Unavailable",
	"common.success": "Success",
	"common.failed": "Failed",
	"common.skipped": "Skipped",
	"common.eligible": "Eligible",
	"common.ineligible": "Ineligible",
	"common.automatic": "Automatic",
	"common.manual": "Manual",
	"common.manual_override": "Manual override",
	"common.cooling_down": "Cooling down",
	"common.stored": "Stored",
	"common.missing": "Missing",
	"common.attempt": "Attempt {n}",
	"common.credential": "Credential #{id}",
	"common.credentialCol": "Credential",
	"common.attemptCol": "Attempt",
	"common.channel": "Channel",
	"common.site": "Site",
	"common.model": "Model",
	"common.priority": "Priority",
	"common.weight": "Weight",
	"common.notes": "Notes",
	"common.baseUrl": "Base URL",
	"assets.siteBaseUrlHint":
		"Site root preferred (https://host). /v1 is also accepted and will not be doubled.",
	"common.platform": "Platform",
	"common.updated": "Updated",
	"common.created": "Created",
	"common.id": "ID",
	"common.kind": "Kind",
	"common.secret": "Secret",
	"common.scopes": "Scopes",
	"common.endpoint": "Endpoint",
	"common.models": "Models",
	"common.group": "Group",
	"common.type": "Type",
	"common.available": "Available",
	"common.checked": "Checked",
	"common.request": "Request",
	"common.errorBrief": "Error",
	"common.actor": "Actor",
	"common.outcome": "Outcome",
	"common.resource": "Resource",
	"common.action": "Action",
	"common.size": "Size",
	"common.duration": "Duration",
	"common.checksum": "Checksum",
	"common.ownership": "Ownership",
	"common.health": "Health",
	"common.member": "Member",
	"common.reasons": "Reasons",
	"common.scheduled": "Scheduled",
	"common.failures": "{n} failures",
	"common.routeId": "Route #{id}",
	"common.channelId": "Channel #{id}",
	"common.siteId": "Site #{id}",
	"common.credentialId": "Credential #{id}",
	"common.memberId": "Member #{id}",
	"common.idHash": "#{id}",
	"common.selectedPriority": "Selected priority: {value}",
	"common.noneValue": "none",
	"common.ms": "{n} ms",
	"common.dotJoin": "{a} · {b}",

	"status.enabled": "Enabled",
	"status.disabled": "Disabled",
	"status.ready": "Ready",
	"status.unavailable": "Unavailable",
	"status.success": "Success",
	"status.failed": "Failed",
	"status.skipped": "Skipped",
	"status.eligible": "Eligible",
	"status.ineligible": "Ineligible",
	"status.automatic": "Automatic",
	"status.manual": "Manual",
	"status.manual_override": "Manual override",
	"status.cooling_down": "Cooling down",
	"status.stored": "Stored",
	"status.missing": "Missing",
	"status.true": "Enabled",
	"status.false": "Disabled",

	"app.brand": "Meta Gateway",
	"app.console": "Admin Console",
	"app.connect.title": "Meta Gateway",
	"app.connect.subtitle":
		"Use ADMIN_TOKEN to open the multi-channel relay console.",
	"app.connect.token": "Admin token",
	"app.connect.remember": "Remember for this browser tab",
	"app.connect.submit": "Connect",
	"app.connect.connecting": "Connecting...",
	"app.connect.failed": "Connection failed",
	"app.connect.hint":
		"Token stays in memory, or this tab session only. Never put it in the URL.",
	"app.nav.dashboard": "Dashboard",
	"app.nav.assets": "Assets",
	"app.nav.routing": "Routing",
	"app.nav.operations": "Operations",
	"app.nav.exchange": "Exchange",
	"app.nav.open": "Open navigation",
	"app.nav.close": "Close navigation",
	"app.ready": "Gateway ready",
	"app.notReady": "Gateway unavailable",
	"app.sessionActive": "Admin session active",
	"app.disconnect": "Disconnect",

	"dashboard.title": "Dashboard",
	"dashboard.description":
		"Gateway health, inventory, and recent operational activity.",
	"dashboard.sites": "Sites",
	"dashboard.channels": "Channels",
	"dashboard.routes": "Routes",
	"dashboard.keys": "Downstream keys",
	"dashboard.proxy": "Recent proxy attempts",
	"dashboard.checkins": "Recent check-ins",
	"dashboard.audit": "Recent admin activity",

	"assets.title": "Assets",
	"assets.description":
		"Manage upstream providers, credentials, relay channels, and client access keys.",
	"assets.tab.sites": "Sites",
	"assets.tab.credentials": "Credentials",
	"assets.tab.channels": "Channels",
	"assets.tab.keys": "Downstream Keys",
	"assets.addSite": "Add site",
	"assets.editSite": "Edit site",
	"assets.deleteSite": "Delete site",
	"assets.deleteSiteMsg": "Delete {name} and its dependent assets?",
	"assets.addCredential": "Add credential",
	"assets.deleteCredential": "Delete credential",
	"assets.deleteCredentialMsg":
		"Delete credential #{id}? Channels using it may become unavailable.",
	"assets.storeCredential": "Store credential",
	"assets.runCheckin": "Run check-in",
	"assets.checkinResult": "Credential #{id}: {message}{reward}",
	"assets.needSite": "Create a site before adding credentials.",
	"assets.needSiteForChannel": "Create a site before adding channels.",
	"assets.secretMetaHint": 'Optional, for example {"platform_user_id":123}',
	"assets.metaJson": "Metadata JSON",
	"assets.kind.api_key": "API key",
	"assets.kind.session": "Session",
	"assets.kind.access_token": "Access token",
	"assets.kind.password": "Password",
	"assets.addChannel": "Add channel",
	"assets.editChannel": "Edit channel",
	"assets.deleteChannel": "Delete channel",
	"assets.deleteChannelMsg": "Delete {name} and its route memberships?",
	"assets.refreshDiscovery": "Refresh discovery",
	"assets.refreshResult": "Found {models} models; created {routes} routes.",
	"assets.refreshResultChannel":
		"Channel #{id}: found {models} models; created {routes} routes.",
	"assets.priorityWeight": "Priority / Weight",
	"assets.modelsCsv": "Models (comma separated)",
	"assets.credentialId": "Credential ID",
	"assets.createKey": "Create key",
	"assets.createDownstreamKey": "Create downstream key",
	"assets.deleteKey": "Delete key",
	"assets.revokeKey": "Revoke downstream key",
	"assets.revokeKeyMsg":
		"Clients using this key will immediately lose gateway access.",
	"assets.revokeKeyConfirm": "Revoke key",
	"assets.copyKeyTitle": "Copy your downstream key",
	"assets.copyKeyWarning":
		"This token is shown once. It cannot be recovered after this dialog closes.",
	"assets.copyToken": "Copy token",
	"assets.storedKey": "I have stored it",

	"routing.title": "Routing",
	"routing.description":
		"Control exact-model routes, channel membership, and live eligibility.",
	"routing.tab.routes": "Routes",
	"routing.tab.explain": "Explain",
	"routing.routes": "Routes",
	"routing.addRoute": "Add route",
	"routing.editRoute": "Edit route",
	"routing.deleteRoute": "Delete route",
	"routing.deleteRouteMsg": "Delete route {name} and all memberships?",
	"routing.routeDetails": "Route details",
	"routing.selectRoute": "Select a route.",
	"routing.addMember": "Add member",
	"routing.editMember": "Edit member",
	"routing.deleteMember": "Delete member",
	"routing.deleteMemberMsg": "Remove channel #{id} from this route?",
	"routing.exactModel": "Exact model",
	"routing.routeEnabled": "Route enabled",
	"routing.protectDiscovery": "Protect from discovery",
	"routing.explainTitle": "Route Explain",
	"routing.modelPlaceholder": "Enter an exact model name",
	"routing.explain": "Explain",
	"routing.explainEmpty": "Enter a model to inspect routing eligibility.",
	"routing.explainNoCandidates":
		"No route members evaluated for this model. Check that an exact route exists and has channel members.",
	"routing.priorityWeight": "Priority / Weight",

	"ops.title": "Operations",
	"ops.description": "Run maintenance tasks and inspect gateway activity.",
	"ops.tab.discovery": "Discovery",
	"ops.tab.checkins": "Check-ins",
	"ops.tab.proxy": "Proxy Logs",
	"ops.tab.audit": "Audit",
	"ops.tab.backups": "Backups",
	"ops.allChannels": "All channels",
	"ops.filterChannel": "Filter channel",
	"ops.refreshAll": "Refresh all",
	"ops.refreshChannel": "Refresh channel",
	"ops.refreshing": "Refreshing...",
	"ops.refreshSummary": "{success} succeeded, {failure} failed",
	"ops.refreshFailures": "Failed channels: {channels}",
	"ops.refreshChannelResult":
		"Channel #{id}: found {models} models; created {routes} routes.",
	"ops.allStatuses": "All statuses",
	"ops.statusFilter": "Status filter",
	"ops.runEnabled": "Run enabled",
	"ops.running": "Running...",
	"ops.checkinSummary":
		"{success} succeeded · {failure} failed · {skipped} skipped",
	"ops.applyRetention": "Apply retention",
	"ops.applyRetentionTitle": "Apply audit retention",
	"ops.applyRetentionMsg":
		"Remove audit events beyond the server's configured age and row limits? This cannot be undone.",
	"ops.runCleanup": "Run cleanup",
	"ops.olderEvents": "Older events",
	"ops.newestEvents": "Newest events",
	"ops.createBackup": "Create backup",
	"ops.creatingBackup": "Creating...",
	"ops.backupCreated": "Backup {name} ready ({size}, {time})",
	"ops.noBackups": "No backups have been created.",
	"ops.restoreNote":
		"Restore is intentionally offline. Use {cmd} while the server is stopped.",

	"exchange.title": "Exchange",
	"exchange.description":
		"Move channel assets through the versioned Meta Gateway exchange format.",
	"exchange.exportTitle": "Export channels",
	"exchange.exportHint":
		"Metadata exports are safe to inspect but cannot be imported. Secret exports are importable and must be handled as credentials.",
	"exchange.allChannels": "All channels",
	"exchange.downloadMetadata": "Download metadata",
	"exchange.exportSecrets": "Export with secrets",
	"exchange.importTitle": "Import assets",
	"exchange.chooseFile": "Choose a JSON exchange file",
	"exchange.maxSize": "Maximum 10 MiB",
	"exchange.readyImport": "JSON parsed and ready to import.",
	"exchange.parseError":
		"Could not parse this file as JSON. Choose a valid exchange document.",
	"exchange.import": "Import assets",
	"exchange.importing": "Importing...",
	"exchange.importComplete": "Import complete",
	"exchange.created": "Created",
	"exchange.updated": "Updated",
	"exchange.adopted": "Adopted",
	"exchange.discoveryFailures": "Discovery failures",
	"exchange.secretTitle": "Export channel secrets?",
	"exchange.secretBody":
		"This file contains plaintext upstream API keys. Store it securely, do not commit it, and delete it when no longer needed. Its contents will not be previewed here.",
	"exchange.downloadSensitive": "Download sensitive export",

	"api.unreachable": "Unable to reach Meta Gateway",
};

const zh: Dict = {
	"lang.en": "English",
	"lang.zh": "中文",
	"lang.switch": "语言",

	"common.cancel": "取消",
	"common.save": "保存",
	"common.create": "创建",
	"common.delete": "删除",
	"common.edit": "编辑",
	"common.retry": "重试",
	"common.close": "关闭",
	"common.loading": "加载中",
	"common.empty": "暂无数据。",
	"common.error": "出错了",
	"common.working": "处理中...",
	"common.none": "无",
	"common.select": "请选择",
	"common.actions": "操作",
	"common.status": "状态",
	"common.name": "名称",
	"common.time": "时间",
	"common.latency": "延迟",
	"common.source": "来源",
	"common.category": "类别",
	"common.reward": "奖励",
	"common.enabled": "已启用",
	"common.disabled": "已禁用",
	"common.ready": "就绪",
	"common.unavailable": "不可用",
	"common.success": "成功",
	"common.failed": "失败",
	"common.skipped": "已跳过",
	"common.eligible": "可用",
	"common.ineligible": "不可用",
	"common.automatic": "自动",
	"common.manual": "手动",
	"common.manual_override": "手动保护",
	"common.cooling_down": "冷却中",
	"common.stored": "已存储",
	"common.missing": "缺失",
	"common.attempt": "第 {n} 次",
	"common.credential": "凭证 #{id}",
	"common.credentialCol": "凭证",
	"common.attemptCol": "尝试",
	"common.channel": "通道",
	"common.site": "站点",
	"common.model": "模型",
	"common.priority": "优先级",
	"common.weight": "权重",
	"common.notes": "备注",
	"common.baseUrl": "基础 URL",
	"assets.siteBaseUrlHint":
		"建议填站点根地址（https://host）。若带 /v1 也可，不会重复拼接。",
	"common.platform": "平台",
	"common.updated": "更新时间",
	"common.created": "创建时间",
	"common.id": "ID",
	"common.kind": "类型",
	"common.secret": "密钥",
	"common.scopes": "权限范围",
	"common.endpoint": "端点",
	"common.models": "模型",
	"common.group": "分组",
	"common.type": "类型",
	"common.available": "可用",
	"common.checked": "检查时间",
	"common.request": "请求",
	"common.errorBrief": "错误",
	"common.actor": "操作者",
	"common.outcome": "结果",
	"common.resource": "资源",
	"common.action": "操作",
	"common.size": "大小",
	"common.duration": "耗时",
	"common.checksum": "校验和",
	"common.ownership": "归属",
	"common.health": "健康",
	"common.member": "成员",
	"common.reasons": "原因",
	"common.scheduled": "定时",
	"common.failures": "{n} 次失败",
	"common.routeId": "路由 #{id}",
	"common.channelId": "通道 #{id}",
	"common.siteId": "站点 #{id}",
	"common.credentialId": "凭证 #{id}",
	"common.memberId": "成员 #{id}",
	"common.idHash": "#{id}",
	"common.selectedPriority": "选中优先级：{value}",
	"common.noneValue": "无",
	"common.ms": "{n} 毫秒",
	"common.dotJoin": "{a} · {b}",

	"status.enabled": "已启用",
	"status.disabled": "已禁用",
	"status.ready": "就绪",
	"status.unavailable": "不可用",
	"status.success": "成功",
	"status.failed": "失败",
	"status.skipped": "已跳过",
	"status.eligible": "可用",
	"status.ineligible": "不可用",
	"status.automatic": "自动",
	"status.manual": "手动",
	"status.manual_override": "手动保护",
	"status.cooling_down": "冷却中",
	"status.stored": "已存储",
	"status.missing": "缺失",
	"status.true": "已启用",
	"status.false": "已禁用",

	"app.brand": "Meta Gateway",
	"app.console": "管理控制台",
	"app.connect.title": "Meta Gateway",
	"app.connect.subtitle": "使用 ADMIN_TOKEN 进入多通道路由运维台。",
	"app.connect.token": "管理员令牌",
	"app.connect.remember": "在此浏览器标签页中记住",
	"app.connect.submit": "连接",
	"app.connect.connecting": "连接中...",
	"app.connect.failed": "连接失败",
	"app.connect.hint": "令牌只保存在内存，或仅限当前标签页会话；不要放进 URL。",
	"app.nav.dashboard": "仪表盘",
	"app.nav.assets": "资产",
	"app.nav.routing": "路由",
	"app.nav.operations": "运维",
	"app.nav.exchange": "交换",
	"app.nav.open": "打开导航",
	"app.nav.close": "关闭导航",
	"app.ready": "网关就绪",
	"app.notReady": "网关不可用",
	"app.sessionActive": "管理员会话已激活",
	"app.disconnect": "断开连接",

	"dashboard.title": "仪表盘",
	"dashboard.description": "网关健康状态、资产清单与近期运维活动。",
	"dashboard.sites": "站点",
	"dashboard.channels": "通道",
	"dashboard.routes": "路由",
	"dashboard.keys": "下游密钥",
	"dashboard.proxy": "最近代理请求",
	"dashboard.checkins": "最近签到",
	"dashboard.audit": "最近管理操作",

	"assets.title": "资产",
	"assets.description": "管理上游服务商、凭证、中继通道与客户端访问密钥。",
	"assets.tab.sites": "站点",
	"assets.tab.credentials": "凭证",
	"assets.tab.channels": "通道",
	"assets.tab.keys": "下游密钥",
	"assets.addSite": "添加站点",
	"assets.editSite": "编辑站点",
	"assets.deleteSite": "删除站点",
	"assets.deleteSiteMsg": "删除 {name} 及其依赖资产？",
	"assets.addCredential": "添加凭证",
	"assets.deleteCredential": "删除凭证",
	"assets.deleteCredentialMsg": "删除凭证 #{id}？使用它的通道可能变为不可用。",
	"assets.storeCredential": "保存凭证",
	"assets.runCheckin": "执行签到",
	"assets.checkinResult": "凭证 #{id}：{message}{reward}",
	"assets.needSite": "请先创建站点，再添加凭证。",
	"assets.needSiteForChannel": "请先创建站点，再添加通道。",
	"assets.secretMetaHint": '可选，例如 {"platform_user_id":123}',
	"assets.metaJson": "元数据 JSON",
	"assets.kind.api_key": "API 密钥",
	"assets.kind.session": "会话",
	"assets.kind.access_token": "访问令牌",
	"assets.kind.password": "密码",
	"assets.addChannel": "添加通道",
	"assets.editChannel": "编辑通道",
	"assets.deleteChannel": "删除通道",
	"assets.deleteChannelMsg": "删除 {name} 及其路由成员？",
	"assets.refreshDiscovery": "刷新模型发现",
	"assets.refreshResult": "发现 {models} 个模型；创建了 {routes} 条路由。",
	"assets.refreshResultChannel":
		"通道 #{id}：发现 {models} 个模型；创建了 {routes} 条路由。",
	"assets.priorityWeight": "优先级 / 权重",
	"assets.modelsCsv": "模型（逗号分隔）",
	"assets.credentialId": "凭证 ID",
	"assets.createKey": "创建密钥",
	"assets.createDownstreamKey": "创建下游密钥",
	"assets.deleteKey": "删除密钥",
	"assets.revokeKey": "吊销下游密钥",
	"assets.revokeKeyMsg": "使用此密钥的客户端将立即失去网关访问权限。",
	"assets.revokeKeyConfirm": "吊销密钥",
	"assets.copyKeyTitle": "复制下游密钥",
	"assets.copyKeyWarning": "此令牌仅显示一次。关闭对话框后将无法再次查看。",
	"assets.copyToken": "复制令牌",
	"assets.storedKey": "我已保存",

	"routing.title": "路由",
	"routing.description": "管理精确模型路由、通道成员与实时可用性。",
	"routing.tab.routes": "路由",
	"routing.tab.explain": "解释",
	"routing.routes": "路由",
	"routing.addRoute": "添加路由",
	"routing.editRoute": "编辑路由",
	"routing.deleteRoute": "删除路由",
	"routing.deleteRouteMsg": "删除路由 {name} 及全部成员？",
	"routing.routeDetails": "路由详情",
	"routing.selectRoute": "请选择一条路由。",
	"routing.addMember": "添加成员",
	"routing.editMember": "编辑成员",
	"routing.deleteMember": "删除成员",
	"routing.deleteMemberMsg": "从该路由移除通道 #{id}？",
	"routing.exactModel": "精确模型",
	"routing.routeEnabled": "启用路由",
	"routing.protectDiscovery": "保护不被发现覆盖",
	"routing.explainTitle": "路由解释",
	"routing.modelPlaceholder": "输入精确模型名称",
	"routing.explain": "解释",
	"routing.explainEmpty": "输入模型以检查路由可用性。",
	"routing.explainNoCandidates":
		"该模型没有可评估的路由成员。请确认存在精确路由且已配置通道成员。",
	"routing.priorityWeight": "优先级 / 权重",

	"ops.title": "运维",
	"ops.description": "执行维护任务并查看网关活动。",
	"ops.tab.discovery": "发现",
	"ops.tab.checkins": "签到",
	"ops.tab.proxy": "代理日志",
	"ops.tab.audit": "审计",
	"ops.tab.backups": "备份",
	"ops.allChannels": "全部通道",
	"ops.filterChannel": "筛选通道",
	"ops.refreshAll": "全部刷新",
	"ops.refreshChannel": "刷新当前通道",
	"ops.refreshing": "刷新中...",
	"ops.refreshSummary": "{success} 成功，{failure} 失败",
	"ops.refreshFailures": "失败通道：{channels}",
	"ops.refreshChannelResult":
		"通道 #{id}：发现 {models} 个模型；创建了 {routes} 条路由。",
	"ops.allStatuses": "全部状态",
	"ops.statusFilter": "状态筛选",
	"ops.runEnabled": "运行已启用项",
	"ops.running": "运行中...",
	"ops.checkinSummary": "{success} 成功 · {failure} 失败 · {skipped} 跳过",
	"ops.applyRetention": "应用保留策略",
	"ops.applyRetentionTitle": "应用审计保留策略",
	"ops.applyRetentionMsg":
		"删除超出服务器配置的时间与行数限制的审计事件？此操作不可撤销。",
	"ops.runCleanup": "执行清理",
	"ops.olderEvents": "更早事件",
	"ops.newestEvents": "最新事件",
	"ops.createBackup": "创建备份",
	"ops.creatingBackup": "创建中...",
	"ops.backupCreated": "备份 {name} 已就绪（{size}，{time}）",
	"ops.noBackups": "尚未创建备份。",
	"ops.restoreNote": "恢复为离线操作。请在服务停止时使用 {cmd}。",

	"exchange.title": "交换",
	"exchange.description": "通过版本化的 Meta Gateway 交换格式迁移通道资产。",
	"exchange.exportTitle": "导出通道",
	"exchange.exportHint":
		"元数据导出走查安全但不可导入。含密钥的导出可导入，必须按凭证妥善保管。",
	"exchange.allChannels": "全部通道",
	"exchange.downloadMetadata": "下载元数据",
	"exchange.exportSecrets": "导出含密钥",
	"exchange.importTitle": "导入资产",
	"exchange.chooseFile": "选择 JSON 交换文件",
	"exchange.maxSize": "最大 10 MiB",
	"exchange.readyImport": "JSON 已解析，可开始导入。",
	"exchange.parseError": "无法将此文件解析为 JSON。请选择有效的交换文档。",
	"exchange.import": "导入资产",
	"exchange.importing": "导入中...",
	"exchange.importComplete": "导入完成",
	"exchange.created": "新建",
	"exchange.updated": "更新",
	"exchange.adopted": "接管",
	"exchange.discoveryFailures": "发现失败",
	"exchange.secretTitle": "导出通道密钥？",
	"exchange.secretBody":
		"此文件包含明文上游 API 密钥。请妥善保管，勿提交到版本库，用完后删除。此处不会预览其内容。",
	"exchange.downloadSensitive": "下载敏感导出",

	"api.unreachable": "无法连接 Meta Gateway",
};

const dictionaries: Record<Locale, Dict> = { en, "zh-CN": zh };

export function detectLocale(): Locale {
	try {
		const saved = localStorage.getItem(LOCALE_KEY);
		if (saved === "en" || saved === "zh-CN") return saved;
	} catch {
		/* ignore */
	}
	try {
		const lang = navigator.language || "";
		if (lang.toLowerCase().startsWith("zh")) return "zh-CN";
	} catch {
		/* ignore */
	}
	return "en";
}

export function translate(
	locale: Locale,
	key: string,
	vars?: Record<string, string | number>,
): string {
	const template = dictionaries[locale][key] ?? dictionaries.en[key] ?? key;
	if (!vars) return template;
	return template.replace(/\{(\w+)\}/g, (_, name: string) => {
		const value = vars[name];
		return value === undefined ? `{${name}}` : String(value);
	});
}

export function statusLabel(locale: Locale, value: string | boolean): string {
	if (typeof value === "boolean")
		return translate(locale, value ? "status.true" : "status.false");
	const normalized = String(value).toLowerCase();
	const mapped =
		dictionaries[locale][`status.${normalized}`] ??
		dictionaries.en[`status.${normalized}`];
	return mapped ?? String(value);
}

interface I18nValue {
	locale: Locale;
	setLocale: (locale: Locale) => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
	status: (value: string | boolean) => string;
}

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
	const [locale, setLocaleState] = useState<Locale>(() => detectLocale());

	const setLocale = useCallback((next: Locale) => {
		setLocaleState(next);
		try {
			localStorage.setItem(LOCALE_KEY, next);
		} catch {
			/* ignore */
		}
	}, []);

	useEffect(() => {
		document.documentElement.lang = locale === "zh-CN" ? "zh-CN" : "en";
	}, [locale]);

	const value = useMemo<I18nValue>(
		() => ({
			locale,
			setLocale,
			t: (key, vars) => translate(locale, key, vars),
			status: (v) => statusLabel(locale, v),
		}),
		[locale, setLocale],
	);

	return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
	const value = useContext(I18nContext);
	if (!value) throw new Error("useI18n must be used inside I18nProvider");
	return value;
}

export function LanguageSwitcher({ className = "" }: { className?: string }) {
	const { locale, setLocale, t } = useI18n();
	return (
		<label className={`language-switcher ${className}`.trim()}>
			<span className="sr-only">{t("lang.switch")}</span>
			<select
				aria-label={t("lang.switch")}
				value={locale}
				onChange={(e) => setLocale(e.target.value as Locale)}
			>
				<option value="en">{t("lang.en")}</option>
				<option value="zh-CN">{t("lang.zh")}</option>
			</select>
		</label>
	);
}
