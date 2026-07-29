/**
 * Operator-facing connection / site type options.
 * Values are stored as channel.type_hint and site.platform.
 * Discovery resolves brands via backend adapters.Registry.
 */
export type ConnectionTypeOption = {
	value: string;
	label: string;
	group: "core" | "relay" | "other";
};

export const CONNECTION_TYPE_OPTIONS: ConnectionTypeOption[] = [
	{ value: "openai-compatible", label: "OpenAI Compatible", group: "core" },
	{ value: "new-api", label: "New API", group: "core" },
	{ value: "one-api", label: "One API", group: "core" },
	{ value: "anthropic", label: "Anthropic (Claude Official)", group: "core" },
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

export function connectionTypeLabel(value: string): string {
	const hit = CONNECTION_TYPE_OPTIONS.find((item) => item.value === value);
	return hit?.label ?? value;
}
