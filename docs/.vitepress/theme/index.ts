import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import GatewayFlow from './components/GatewayFlow.vue'
import './custom.css'

/**
 * 站点主题：继承 VitePress 默认主题，只做两件事——
 *   1. 把仓库自己的 Paper / Cobalt 令牌（web/src/styles/tokens.css）映射到 --vp-*；
 *   2. 注册首页用的领域示意图组件。
 * 不重写布局、不 fork 默认主题，避免升级 VitePress 时产生分叉。
 */
export default {
	extends: DefaultTheme,
	enhanceApp({ app }) {
		app.component('GatewayFlow', GatewayFlow)
	},
} satisfies Theme
