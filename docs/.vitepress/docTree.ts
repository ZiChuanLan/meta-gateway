/**
 * 文档树 —— 导航与侧边栏的单一真相源。
 *
 * nav 与 sidebar 都由这份结构派生：新增一页只改这里一行，
 * 不允许在 config.ts 里另写一份页面清单（两份清单必然漂移）。
 *
 * 约定：只登记**真实存在**的页面。VitePress 的侧边栏链接不参与死链检查，
 * 登记一个还没写的页面 = 站点上多一个 404，比少一个入口更糟。
 */

export interface DocPage {
	/** 侧边栏与导航文案 */
	text: string
	/** 站点内绝对链接，不带 .md 后缀、不带结尾斜杠（章节首页除外） */
	link: string
}

export interface DocGroup {
	/** 侧边栏分组标题 */
	text: string
	items: DocPage[]
}

export interface DocSection {
	/** 目录名，同时是 sidebar 的匹配前缀（`/<id>/`） */
	id: string
	/** 导航与侧边栏的章节名 */
	text: string
	/** 章节定位，用于首页与章节页脚 */
	tagline: string
	groups: DocGroup[]
}

/** 页面清单：P0 只收录已经写好或已迁移的页面。 */
export const SECTIONS: DocSection[] = [
	{
		id: 'guide',
		text: '入门',
		tagline: '定位、核心概念与第一条可用的请求',
		groups: [
			{
				text: '起步',
				items: [
					{ text: '概览', link: '/guide/' },
					{ text: '什么是 Meta Gateway', link: '/guide/what-is-meta-gateway' },
					{ text: '核心概念', link: '/guide/concepts' },
				],
			},
			{
				text: '部署',
				items: [
					{ text: 'Docker Compose 部署', link: '/guide/quickstart-docker' },
					{ text: '源码构建运行', link: '/guide/quickstart-source' },
					{ text: 'AI 一键部署', link: '/guide/ai-deploy-prompt' },
					{ text: '升级与更新渠道', link: '/guide/upgrade' },
				],
			},
		],
	},
	{
		id: 'clients',
		text: '接入',
		tagline: '下游协议、鉴权与图像接口',
		groups: [
			{
				text: '下游接入',
				items: [
					{ text: '概览', link: '/clients/' },
					{ text: '鉴权与下游令牌', link: '/clients/auth' },
					{ text: '流式与 SSE 语义', link: '/clients/streaming' },
					{ text: '图像生成与编辑', link: '/clients/image-editing' },
					{ text: '错误语义', link: '/clients/errors' },
				],
			},
		],
	},
	{
		id: 'upstream',
		text: '上游',
		tagline: '站点、渠道、协议映射与模型发现',
		groups: [
			{
				text: '上游接入',
				items: [
					{ text: '概览', link: '/upstream/' },
					{ text: '基础 URL 与端点规则', link: '/upstream/base-url-rules' },
					{ text: '自定义端点映射', link: '/upstream/endpoint-mapping' },
					{ text: '站点探针', link: '/upstream/site-probe' },
				],
			},
		],
	},
	{
		id: 'routing',
		text: '路由',
		tagline: '路由、成员、选型与故障转移',
		groups: [
			{
				text: '路由与模型',
				items: [
					{ text: '概览', link: '/routing/' },
					{ text: '路由选型与故障转移', link: '/routing/selection' },
					{ text: '一键挂载与模型归一', link: '/routing/auto-match' },
					{ text: '模型归一（unify）', link: '/routing/unify' },
					{ text: '模型能力注册表', link: '/routing/capabilities' },
				],
			},
		],
	},
	{
		id: 'billing',
		text: '计费',
		tagline: '单价优先级、配额与用量账单',
		groups: [
			{
				text: '计费与用量',
				items: [
					{ text: '概览', link: '/billing/' },
					{ text: '定价配置', link: '/billing/pricing-setup' },
				],
			},
		],
	},
	{
		id: 'team',
		text: '团队',
		tagline: '个人／团队模式、成员、码与第三方登录',
		groups: [
			{
				text: '团队与用户',
				items: [
					{ text: '概览', link: '/team/' },
					{ text: '个人与团队模式', link: '/team/modes' },
					{ text: '成员与账户', link: '/team/members' },
					{ text: '团队码', link: '/team/codes' },
					{ text: '第三方登录', link: '/team/oauth' },
					{ text: '路由方案与策略', link: '/team/routing-and-policies' },
				],
			},
		],
	},
	{
		id: 'console',
		text: '控制台',
		tagline: '信息架构、主题包与交互约定',
		groups: [
			{
				text: '控制台',
				items: [
					{ text: '概览', link: '/console/' },
					{ text: '主题包', link: '/console/themes' },
					{ text: '视觉与工作区设计', link: '/console/visual-design' },
					{ text: '交互约定', link: '/console/interactions' },
					{ text: '界面入口自定义', link: '/console/entrances' },
					{ text: '列表页状态', link: '/console/list-state' },
					{ text: '工作台', link: '/console/workbench' },
				],
			},
		],
	},
	{
		id: 'operations',
		text: '运维',
		tagline: '架构、配置、数据、备份与排查',
		groups: [
			{
				text: '运维',
				items: [
					{ text: '概览', link: '/operations/' },
					{ text: '架构总览', link: '/operations/architecture' },
					{ text: '部署与反代', link: '/operations/deployment' },
					{ text: '安全', link: '/operations/security' },
					{ text: '观测与指标', link: '/operations/observability' },
					{ text: '告警', link: '/operations/alerts' },
					{ text: '审计与日志', link: '/operations/audit-and-logs' },
					{ text: '数据保留策略', link: '/operations/retention' },
					{ text: '维护与清理', link: '/operations/maintenance' },
					{ text: '备份与恢复', link: '/operations/backup-restore' },
					{ text: 'WebDAV 同步', link: '/operations/webdav-sync' },
					{ text: '故障排查', link: '/operations/troubleshooting' },
					{ text: '端到端验证', link: '/operations/testing' },
				],
			},
		],
	},
	{
		id: 'plugins',
		text: '插件',
		tagline: 'sidecar 协议、安装、配置与拦截钩子',
		groups: [
			{
				text: '插件',
				items: [
					{ text: '概览', link: '/plugins/' },
					{ text: '拦截钩子', link: '/plugins/hooks' },
					{ text: '开发插件', link: '/plugins/develop' },
					{ text: '安装与市场', link: '/plugins/install' },
				],
			},
		],
	},
	{
		id: 'reference',
		text: '参考',
		tagline: '由代码生成的配置、接口与错误码全表',
		groups: [
			{
				text: '参考',
				items: [
					{ text: '概览', link: '/reference/' },
					{ text: '环境变量全表', link: '/reference/env-vars' },
					{ text: '运行设置全表', link: '/reference/runtime-settings' },
					{ text: '管理 API 全表', link: '/reference/admin-api' },
					{ text: '公开接口全表', link: '/reference/public-api' },
					{ text: '错误码与分类', link: '/reference/error-codes' },
					{ text: '数据表与迁移', link: '/reference/database-schema' },
					{ text: '连接类型全表', link: '/reference/connection-types' },
					{ text: '供应商 profile', link: '/reference/provider-profiles' },
				],
			},
		],
	},
]

/** 顶栏导航分组：把 SECTIONS 聚合成 6 个下拉。 */
export const NAV: { text: string; sections: string[] }[] = [
	{ text: '入门', sections: ['guide'] },
	{ text: '使用', sections: ['clients', 'console'] },
	{ text: '配置', sections: ['upstream', 'routing', 'billing'] },
	{ text: '团队', sections: ['team'] },
	{ text: '运维', sections: ['operations', 'plugins'] },
	{ text: '参考', sections: ['reference'] },
]

export function sectionById(id: string): DocSection {
	const found = SECTIONS.find((s) => s.id === id)
	if (!found) throw new Error(`docTree: 未知章节 "${id}"`)
	return found
}

/** 章节的全部页面，按分组顺序展平。 */
export function pagesOf(section: DocSection): DocPage[] {
	return section.groups.flatMap((g) => g.items)
}
