import { defineConfig, type DefaultTheme } from 'vitepress'
import { NAV, SECTIONS, pagesOf, sectionById } from './docTree'

/**
 * Pages 项目站点的子路径。本地预览或换域名时用 DOCS_BASE 覆盖：
 *   $env:DOCS_BASE='/'; npm run build
 * 漏了这个 base，线上就是全站资源 404。
 */
const base = process.env.DOCS_BASE || '/meta-gateway/'

const REPO = 'https://github.com/ZiChuanLan/meta-gateway'

/** 章节的侧边栏：分组直接来自 docTree，不另写一份页面清单。 */
function sidebarFor(sectionId: string): DefaultTheme.SidebarItem[] {
	const section = sectionById(sectionId)
	return section.groups.map((group) => ({
		text: group.text,
		items: group.items.map((page) => ({ text: page.text, link: page.link })),
	}))
}

const sidebar: DefaultTheme.Sidebar = Object.fromEntries(
	SECTIONS.map((section) => [`/${section.id}/`, sidebarFor(section.id)]),
)

/** 顶栏导航：单章节直接跳转，多章节折叠成分组下拉。 */
const nav: DefaultTheme.NavItem[] = NAV.map((entry) => {
	if (entry.sections.length === 1) {
		const section = sectionById(entry.sections[0])
		return { text: entry.text, link: `/${section.id}/`, activeMatch: `/${section.id}/` }
	}
	return {
		text: entry.text,
		items: entry.sections.map((id) => {
			const section = sectionById(id)
			return {
				text: section.text,
				items: pagesOf(section).map((page) => ({ text: page.text, link: page.link })),
			}
		}),
	}
})

/**
 * 中文字典：VitePress 内置文案是英文，逐条覆盖。
 * 这里只列站点实际会渲染出来的标签，不照抄整份字典。
 */
const zhTheme: DefaultTheme.Config = {
	nav,
	sidebar,
	outline: { level: [2, 3], label: '本页目录' },
	docFooter: { prev: '上一页', next: '下一页' },
	lastUpdated: { text: '最后更新于', formatOptions: { dateStyle: 'short', timeStyle: 'short' } },
	darkModeSwitchLabel: '外观',
	sidebarMenuLabel: '目录',
	returnToTopLabel: '回到顶部',
	externalLinkIcon: true,
	editLink: {
		pattern: `${REPO}/edit/master/docs/:path`,
		text: '在 GitHub 上编辑此页',
	},
	notFound: {
		title: '页面不存在',
		quote: '这个地址没有对应的文档。',
		linkText: '回到首页',
		linkLabel: '回到首页',
	},
	footer: {
		message: '本仓库遵循仓库根目录 LICENSE 中的许可条款。',
		copyright: 'Meta Gateway — 自托管 AI 网关',
	},
}

export default defineConfig({
	base,
	srcExclude: ['node_modules/**', 'marketing/**', 'screenshots/**', 'icons/**', 'public/**'],
	title: 'Meta Gateway',
	description: '自托管 AI 网关与管理控制台：多渠道路由、协议互译、用量计费与审计。',
	lang: 'zh-CN',
	cleanUrls: true,
	lastUpdated: true,
	head: [
		['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}logo.svg` }],
		['meta', { name: 'theme-color', content: '#f3f4f6' }],
	],

	/**
	 * 内链死链直接让构建失败。这与仓库既有的 parity.test.ts / 投影列一致性测试
	 * 是同一套思路：让"文档指错了地方"在 CI 里暴露，而不是等读者点开才发现。
	 */
	markdown: {
		deadLink: 'error',
		lineNumbers: false,
	},

	themeConfig: {
		logo: '/logo.svg',
		siteTitle: false,
		socialLinks: [{ icon: 'github', link: REPO }],
		search: {
			provider: 'local',
			options: {
				detailedView: true,
				translations: {
					button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
					modal: {
						displayDetails: '展开详情',
						resetButtonTitle: '清除查询',
						backButtonTitle: '关闭搜索',
						noResultsText: '没有匹配结果',
						footer: {
							selectText: '选择',
							selectKeyAriaLabel: '回车',
							navigateText: '切换',
							navigateUpKeyAriaLabel: '上箭头',
							navigateDownKeyAriaLabel: '下箭头',
							closeText: '关闭',
							closeKeyAriaLabel: 'Esc',
						},
					},
				},
				/*
				 * VitePress 默认分词按空白切，中文整段会被当成一个 token，
				 * 搜"路由"搜不到"路由与成员"。这里把 CJK 逐字切分、拉丁词保持完整，
				 * 是中文文档站可用的最小改动。
				 */
				miniSearch: {
					options: {
						tokenize: (text: string) =>
							text
								.split(/[\s\-_/.,:;()\[\]{}]+/)
								.flatMap((part) => part.match(/[\u4e00-\u9fff]|[a-zA-Z0-9.]+/g) ?? [])
								.filter(Boolean),
					},
				},
			},
		},
	},

	/*
	 * 语言：中文为写作基线，`en` 只留结构位。
	 * 将来加英文版 = 在 locales 里加一段 + 建 docs/en/ 目录，
	 * 不需要移动任何中文文件（中文内容就在站点根，英文挂在 /en/ 前缀下）。
	 */
	locales: {
		root: {
			label: '简体中文',
			lang: 'zh-CN',
			title: 'Meta Gateway',
			description: '自托管 AI 网关与管理控制台：多渠道路由、协议互译、用量计费与审计。',
			themeConfig: zhTheme,
		},
	},
})
