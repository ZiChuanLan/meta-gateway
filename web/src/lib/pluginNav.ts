import { useEffect, useState } from "react";

const STORAGE_KEY = "meta-gateway.plugin-nav-hidden";
// Same-tab listeners need an event of their own: the `storage` event only fires
// in other tabs, so hiding a plugin would not move the sidebar until reload.
const CHANGE_EVENT = "meta-gateway:plugin-nav-changed";

/**
 * Which plugins are hidden from the sidebar.
 *
 * A UI preference, not plugin state: hiding a nav entry says nothing about the
 * plugin, and it must survive an unrelated reload of the plugin (a toggle in
 * the database would be lost the moment a plugin is reinstalled). It follows
 * the same rule as the theme, the language and the collapsed sidebar.
 */
export function readHiddenPlugins(): Set<string> {
	try {
		const raw = localStorage.getItem(STORAGE_KEY);
		if (!raw) return new Set();
		const parsed: unknown = JSON.parse(raw);
		if (!Array.isArray(parsed)) return new Set();
		return new Set(parsed.filter((id): id is string => typeof id === "string"));
	} catch {
		// Storage disabled or the value was hand-edited: show everything.
		return new Set();
	}
}

export function setPluginNavHidden(id: string, hidden: boolean): void {
	const next = readHiddenPlugins();
	if (hidden) next.add(id);
	else next.delete(id);
	try {
		localStorage.setItem(STORAGE_KEY, JSON.stringify([...next]));
	} catch {
		/* storage unavailable: the choice just does not persist */
	}
	window.dispatchEvent(new Event(CHANGE_EVENT));
}

/** Subscribes to the hidden set, so the sidebar reacts without a reload. */
export function useHiddenPlugins(): Set<string> {
	const [hidden, setHidden] = useState<Set<string>>(readHiddenPlugins);
	useEffect(() => {
		const sync = () => setHidden(readHiddenPlugins());
		window.addEventListener(CHANGE_EVENT, sync);
		window.addEventListener("storage", sync);
		return () => {
			window.removeEventListener(CHANGE_EVENT, sync);
			window.removeEventListener("storage", sync);
		};
	}, []);
	return hidden;
}
