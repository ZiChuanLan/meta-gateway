import { Download, FileJson, Upload } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { api } from "../api/client";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	Button,
	Dialog,
	ErrorState,
	Page,
	Panel,
	StatusBadge,
} from "../components/ui";

export function Exchange() {
	const { client } = useSession();
	const { t } = useI18n();
	const s = api(client!);
	const qc = useQueryClient();
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => s.channels(signal),
	});
	const [selected, setSelected] = useState<number[]>([]);
	const [secretWarning, setSecretWarning] = useState(false);
	const [fileName, setFileName] = useState("");
	const [document, setDocument] = useState<unknown>(null);
	const [parseError, setParseError] = useState<string | null>(null);
	const input = useRef<HTMLInputElement>(null);
	const exp = useMutation({
		mutationFn: async ({ secrets }: { secrets: boolean }) => {
			const data = await s.exportData(secrets, selected);
			const blob = new Blob([JSON.stringify(data, null, 2)], {
				type: "application/json",
			});
			const url = URL.createObjectURL(blob);
			const a = window.document.createElement("a");
			a.href = url;
			a.download = `meta-gateway-${secrets ? "secret-" : "metadata-"}export.json`;
			a.click();
			URL.revokeObjectURL(url);
		},
		onSuccess: () => setSecretWarning(false),
	});
	const imp = useMutation({
		mutationFn: () => s.importData(document),
		onSuccess: () => {
			// Keep import result counts on the mutation data while refreshing
			// every asset surface that exchange can create or update.
			void qc.invalidateQueries({ queryKey: ["sites"] });
			void qc.invalidateQueries({ queryKey: ["credentials"] });
			void qc.invalidateQueries({ queryKey: ["channels"] });
			void qc.invalidateQueries({ queryKey: ["routes"] });
			void qc.invalidateQueries({ queryKey: ["keys"] });
			void qc.invalidateQueries({ queryKey: ["models"] });
			void qc.invalidateQueries({ queryKey: ["proxy-logs"] });
			void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
			setDocument(null);
			setFileName("");
			setParseError(null);
			if (input.current) input.current.value = "";
		},
	});
	async function choose(file?: File) {
		if (!file) return;
		setFileName(file.name);
		setParseError(null);
		try {
			setDocument(JSON.parse(await file.text()));
		} catch {
			setDocument(null);
			setParseError(t("exchange.parseError"));
		}
	}
	return (
		<Page title={t("exchange.title")} description={t("exchange.description")}>
			<div className="exchange-grid">
				<Panel title={t("exchange.exportTitle")}>
					<p>{t("exchange.exportHint")}</p>
					<div className="selection-list">
						<label className="check">
							<input
								type="checkbox"
								checked={selected.length === 0}
								onChange={() => setSelected([])}
							/>
							<span>{t("exchange.allChannels")}</span>
						</label>
						{channels.data?.map((c) => (
							<label className="check" key={c.id}>
								<input
									type="checkbox"
									checked={selected.includes(c.id)}
									onChange={(e) =>
										setSelected(
											e.target.checked
												? [...selected, c.id]
												: selected.filter((id) => id !== c.id),
										)
									}
								/>
								<span>{c.name}</span>
							</label>
						))}
					</div>
					{exp.error && <ErrorState error={exp.error} />}
					<div className="toolbar">
						<Button
							variant="secondary"
							icon={<Download size={16} />}
							disabled={exp.isPending}
							onClick={() => exp.mutate({ secrets: false })}
						>
							{t("exchange.downloadMetadata")}
						</Button>
						<Button
							variant="danger"
							icon={<Download size={16} />}
							disabled={exp.isPending}
							onClick={() => setSecretWarning(true)}
						>
							{t("exchange.exportSecrets")}
						</Button>
					</div>
				</Panel>
				<Panel title={t("exchange.importTitle")}>
					<div className="drop-zone" onClick={() => input.current?.click()}>
						<FileJson size={28} />
						<strong>{fileName || t("exchange.chooseFile")}</strong>
						<span>{t("exchange.maxSize")}</span>
						<input
							ref={input}
							hidden
							type="file"
							accept="application/json,.json"
							onChange={(e) => choose(e.target.files?.[0])}
						/>
					</div>
					{parseError && <ErrorState error={parseError} />}
					{document !== null && (
						<div className="result-strip">
							<StatusBadge value="ready" />
							<span>{t("exchange.readyImport")}</span>
						</div>
					)}
					{imp.error && <ErrorState error={imp.error} />}
					<Button
						icon={<Upload size={16} />}
						disabled={!document || imp.isPending}
						onClick={() => imp.mutate()}
					>
						{imp.isPending ? t("exchange.importing") : t("exchange.import")}
					</Button>
					{imp.data && (
						<div className="import-result">
							<h3>{t("exchange.importComplete")}</h3>
							<div>
								<span>
									{t("exchange.created")}{" "}
									<strong>{imp.data.created_count}</strong>
								</span>
								<span>
									{t("exchange.updated")}{" "}
									<strong>{imp.data.updated_count}</strong>
								</span>
								<span>
									{t("exchange.adopted")}{" "}
									<strong>{imp.data.adopted_count}</strong>
								</span>
								<span>
									{t("exchange.discoveryFailures")}{" "}
									<strong>{imp.data.discovery_failure_count}</strong>
								</span>
							</div>
						</div>
					)}
				</Panel>
			</div>
			{secretWarning && (
				<Dialog
					danger
					title={t("exchange.secretTitle")}
					onClose={() => setSecretWarning(false)}
					actions={
						<>
							<Button
								variant="secondary"
								onClick={() => setSecretWarning(false)}
							>
								{t("common.cancel")}
							</Button>
							<Button
								variant="danger"
								disabled={exp.isPending}
								onClick={() => exp.mutate({ secrets: true })}
							>
								{t("exchange.downloadSensitive")}
							</Button>
						</>
					}
				>
					<p>{t("exchange.secretBody")}</p>
				</Dialog>
			)}
		</Page>
	);
}
