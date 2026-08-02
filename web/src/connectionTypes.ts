/**
 * Operator-facing connection / site type options.
 * Values are stored as channel.type_hint and site.platform.
 * Discovery resolves brands via backend adapters.Registry.
 */
export type ConnectionTypeOption = {
	value: string;
	label: string;
	group: "core" | "cn" | "intl" | "relay" | "other";
};

export const CONNECTION_TYPE_OPTIONS: ConnectionTypeOption[] = [
	{ value: "openai-compatible", label: "OpenAI Compatible", group: "core" },
	{ value: "new-api", label: "New API", group: "core" },
	{ value: "one-api", label: "One API", group: "core" },
	{ value: "anthropic", label: "Anthropic (Claude Official)", group: "core" },
	{ value: "gemini", label: "Google Gemini (Official)", group: "core" },
	// Domestic providers — all OpenAI-compatible; pick one and paste an API key.
	{ value: "deepseek", label: "DeepSeek", group: "cn" },
	{ value: "moonshot", label: "Moonshot (Kimi)", group: "cn" },
	{ value: "zhipu", label: "智谱 GLM", group: "cn" },
	{ value: "qwen", label: "通义千问 (DashScope)", group: "cn" },
	{ value: "doubao", label: "豆包 (火山方舟)", group: "cn" },
	{ value: "siliconflow", label: "硅基流动", group: "cn" },
	{ value: "minimax", label: "MiniMax", group: "cn" },
	{ value: "stepfun", label: "阶跃星辰 StepFun", group: "cn" },
	{ value: "lingyiwanwu", label: "零一万物", group: "cn" },
	{ value: "baichuan", label: "百川智能", group: "cn" },
	{ value: "spark", label: "讯飞星火", group: "cn" },
	{ value: "hunyuan", label: "腾讯混元", group: "cn" },
	{ value: "qianfan", label: "百度千帆", group: "cn" },
	// International providers with OpenAI-compatible endpoints.
	{ value: "openrouter", label: "OpenRouter", group: "intl" },
	{ value: "groq", label: "Groq", group: "intl" },
	{ value: "xai", label: "xAI (Grok)", group: "intl" },
	{ value: "mistral", label: "Mistral AI", group: "intl" },
	{ value: "perplexity", label: "Perplexity", group: "intl" },
	{ value: "axonhub", label: "AxonHub", group: "relay" },
	{ value: "metapi", label: "Metapi", group: "relay" },
	{ value: "anyrouter", label: "AnyRouter", group: "relay" },
	{ value: "one-hub", label: "One Hub", group: "relay" },
	{ value: "done-hub", label: "Done Hub", group: "relay" },
	{ value: "veloera", label: "Veloera", group: "relay" },
	{ value: "sub2api", label: "Sub2API", group: "relay" },
	{ value: "aihubmix", label: "AIHubMix", group: "relay" },
	{ value: "sharedchat", label: "SharedChat", group: "relay" },
	{ value: "v-api", label: "V-API", group: "relay" },
	{ value: "voapi", label: "VoAPI", group: "relay" },
	{ value: "super-api", label: "Super-API", group: "relay" },
	{ value: "rix-api", label: "Rix-API", group: "relay" },
	{ value: "neo-api", label: "Neo-API", group: "relay" },
	{ value: "octopus", label: "Octopus", group: "relay" },
	{ value: "claude-code-hub", label: "Claude Code Hub", group: "relay" },
	{ value: "wong-gongyi", label: "Wong Gongyi", group: "relay" },
	{ value: "custom", label: "Custom…", group: "other" },
];

/** Default upstream base URLs per provider (empty = operator fills in). */
export const PROVIDER_BASE_URLS: Record<string, string> = {
	"openai-compatible": "",
	"new-api": "",
	"one-api": "",
	anthropic: "https://api.anthropic.com",
	gemini: "https://generativelanguage.googleapis.com/v1beta",
	deepseek: "https://api.deepseek.com/v1",
	moonshot: "https://api.moonshot.cn/v1",
	zhipu: "https://open.bigmodel.cn/api/paas/v4",
	qwen: "https://dashscope.aliyuncs.com/compatible-mode/v1",
	doubao: "https://ark.cn-beijing.volces.com/api/v3",
	siliconflow: "https://api.siliconflow.cn/v1",
	minimax: "https://api.minimaxi.com/v1",
	stepfun: "https://api.stepfun.com/v1",
	lingyiwanwu: "https://api.lingyiwanwu.com/v1",
	baichuan: "https://api.baichuan-ai.com/v1",
	spark: "https://spark-api-open.xf-yun.com/v1",
	hunyuan: "https://api.hunyuan.cloud.tencent.com/v1",
	qianfan: "https://qianfan.baidubce.com/v2",
	openrouter: "https://openrouter.ai/api/v1",
	groq: "https://api.groq.com/openai/v1",
	xai: "https://api.x.ai/v1",
	mistral: "https://api.mistral.ai/v1",
	perplexity: "https://api.perplexity.ai",
};
