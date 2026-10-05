<script setup lang="ts">
/**
 * 首页的网关链路示意。
 *
 * 装饰必须是领域相关的（这是控制台/门面一贯的约束）：这里画的是真实数据流——
 * 下游请求按 OpenAI / Anthropic / 任意 /v1 路径进来，经网关四个处理阶段，
 * 落到聚合的上游站点。不是泛化的几何背景图。
 */
const downstream = [
	{ label: 'OpenAI SDK', note: '/v1/chat/completions' },
	{ label: 'Anthropic SDK', note: '/v1/messages' },
	{ label: '任意 /v1 路径', note: '未登记端点透传' },
]

const stages = [
	{ label: '路由选型', note: '分组 · 权重 · 粘性' },
	{ label: '协议互译', note: '适配器 · 字段映射' },
	{ label: '计费与配额', note: '单价链 · 额度' },
	{ label: '日志与追踪', note: 'proxy_logs · 指标' },
]

const upstream = [
	{ label: '上游站点 A', note: 'OpenAI 形态' },
	{ label: '上游站点 B', note: '非 OpenAI 形态' },
	{ label: '上游站点 C', note: '自定义端点' },
]
</script>

<template>
	<section class="gw-flow" aria-label="请求链路示意">
		<div class="gw-flow__col">
			<p class="gw-flow__head">下游客户端</p>
			<div v-for="item in downstream" :key="item.label" class="gw-node">
				<span class="gw-node__label">{{ item.label }}</span>
				<code class="gw-node__note">{{ item.note }}</code>
			</div>
		</div>

		<div class="gw-flow__rail" aria-hidden="true">
			<svg viewBox="0 0 40 220" preserveAspectRatio="none">
				<path class="gw-line" d="M0 30 C 20 30, 20 110, 40 110" />
				<path class="gw-line" d="M0 110 L 40 110" />
				<path class="gw-line" d="M0 190 C 20 190, 20 110, 40 110" />
			</svg>
		</div>

		<div class="gw-flow__core">
			<p class="gw-flow__head">Meta Gateway</p>
			<div class="gw-core">
				<p class="gw-core__title">透明 pass-through</p>
				<p class="gw-core__note">不改写请求语义 · 不注入提示词 · 不伪造响应</p>
				<ul class="gw-core__stages">
					<li v-for="stage in stages" :key="stage.label">
						<span>{{ stage.label }}</span>
						<em>{{ stage.note }}</em>
					</li>
				</ul>
			</div>
		</div>

		<div class="gw-flow__rail" aria-hidden="true">
			<svg viewBox="0 0 40 220" preserveAspectRatio="none">
				<path class="gw-line" d="M0 110 C 20 110, 20 30, 40 30" />
				<path class="gw-line" d="M0 110 L 40 110" />
				<path class="gw-line" d="M0 110 C 20 110, 20 190, 40 190" />
			</svg>
		</div>

		<div class="gw-flow__col">
			<p class="gw-flow__head">上游站点</p>
			<div v-for="item in upstream" :key="item.label" class="gw-node">
				<span class="gw-node__label">{{ item.label }}</span>
				<code class="gw-node__note">{{ item.note }}</code>
			</div>
		</div>
	</section>
</template>

<style scoped>
.gw-flow {
	display: grid;
	grid-template-columns: minmax(0, 1fr) 40px minmax(0, 1.35fr) 40px minmax(0, 1fr);
	align-items: center;
	gap: 0;
	margin: 0 auto;
	max-width: 1080px;
	padding: 8px 24px 40px;
}

.gw-flow__col {
	display: flex;
	flex-direction: column;
	gap: 10px;
}

.gw-flow__head {
	margin: 0 0 2px;
	font-size: 11px;
	font-weight: 600;
	letter-spacing: 0.14em;
	text-transform: uppercase;
	color: var(--vp-c-text-3);
}

.gw-node {
	display: flex;
	flex-direction: column;
	gap: 3px;
	padding: 9px 12px;
	border: 1px solid var(--vp-c-divider);
	border-radius: 8px;
	background: var(--vp-c-bg-elv);
}

.gw-node__label {
	font-size: 13px;
	font-weight: 600;
	color: var(--vp-c-text-1);
}

.gw-node__note {
	font-family: var(--vp-font-family-mono);
	font-size: 11px;
	color: var(--vp-c-text-3);
	background: none;
	padding: 0;
}

.gw-flow__rail {
	align-self: stretch;
	display: flex;
}

.gw-flow__rail svg {
	width: 100%;
	height: 100%;
}

.gw-line {
	fill: none;
	stroke: var(--vp-c-brand-1);
	stroke-width: 1.25;
	stroke-dasharray: 5 4;
	opacity: 0.45;
	animation: gw-dash 1.6s linear infinite;
}

@keyframes gw-dash {
	to {
		stroke-dashoffset: -18;
	}
}

.gw-flow__core {
	display: flex;
	flex-direction: column;
}

.gw-core {
	border: 1px solid var(--vp-c-brand-soft);
	border-radius: 10px;
	background: var(--vp-c-bg-elv);
	padding: 16px 18px;
}

.gw-core__title {
	margin: 0;
	font-size: 15px;
	font-weight: 700;
	color: var(--vp-c-text-1);
}

.gw-core__note {
	margin: 4px 0 12px;
	font-size: 12px;
	line-height: 1.5;
	color: var(--vp-c-text-3);
}

.gw-core__stages {
	list-style: none;
	margin: 0;
	padding: 0;
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 8px;
}

.gw-core__stages li {
	display: flex;
	flex-direction: column;
	gap: 2px;
	padding-left: 10px;
	border-left: 2px solid var(--vp-c-brand-soft);
}

.gw-core__stages span {
	font-size: 12.5px;
	font-weight: 600;
	color: var(--vp-c-text-1);
}

.gw-core__stages em {
	font-size: 11px;
	font-style: normal;
	color: var(--vp-c-text-3);
}

@media (max-width: 900px) {
	.gw-flow {
		grid-template-columns: minmax(0, 1fr);
		gap: 14px;
		padding: 8px 24px 32px;
	}

	.gw-flow__rail {
		display: none;
	}

	.gw-core__stages {
		grid-template-columns: minmax(0, 1fr);
	}
}

@media (prefers-reduced-motion: reduce) {
	.gw-line {
		animation: none;
	}
}
</style>
