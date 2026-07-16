import {
	Copy,
	KeyRound,
	Pencil,
	Play,
	Plus,
	RefreshCw,
	Trash2,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { api } from "../api/client";
import type { Channel, CreatedDownstreamKey, Site } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	Button,
	ConfirmDialog,
	DataTable,
	Dialog,
	Empty,
	ErrorState,
	Field,
	IconButton,
	Loading,
	Page,
	Panel,
	StatusBadge,
	Tabs,
	formatDate,
} from "../components/ui";

type Tab = "sites" | "credentials" | "channels" | "keys";

export function Assets() {
	const { t } = useI18n();
	const [tab, setTab] = useState<Tab>("sites");
	return (
		<Page title={t("assets.title")} description={t("assets.description")}>
			<Tabs
				items={[
					{ value: "sites", label: t("assets.tab.sites") },
					{ value: "credentials", label: t("assets.tab.credentials") },
					{ value: "channels", label: t("assets.tab.channels") },
					{ value: "keys", label: t("assets.tab.keys") },
				]}
				active={tab}
				onChange={(v) => setTab(v as Tab)}
			/>
			{tab === "sites" && <Sites />}
			{tab === "credentials" && <Credentials />}
			{tab === "channels" && <Channels />}
			{tab === "keys" && <Keys />}
		</Page>
	);
}

function Sites() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const qc = useQueryClient();
	const query = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => service.sites(signal),
	});
	const [edit, setEdit] = useState<Partial<Site> | null>(null);
	const [remove, setRemove] = useState<Site | null>(null);
	const save = useMutation({
		mutationFn: (value: Partial<Site>) =>
			value.id
				? service.updateSite(value.id, value)
				: service.createSite(value),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["sites"] });
			setEdit(null);
		},
	});
	const del = useMutation({
		mutationFn: (id: number) => service.deleteSite(id),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["sites"] });
			setRemove(null);
		},
	});
	return (
		<Panel
			actions={
				<Button
					icon={<Plus size={16} />}
					onClick={() =>
						setEdit({ status: "enabled", platform: "openai-compatible" })
					}
				>
					{t("assets.addSite")}
				</Button>
			}
		>
			{query.isPending ? (
				<Loading />
			) : query.isError ? (
				<ErrorState error={query.error} />
			) : (
				<DataTable
					headers={[
						t("common.name"),
						t("common.baseUrl"),
						t("common.platform"),
						t("common.status"),
						t("common.updated"),
						t("common.actions"),
					]}
					empty={!query.data.length}
				>
					{query.data.map((s) => (
						<tr key={s.id}>
							<td>
								<strong>{s.name}</strong>
								<small>#{s.id}</small>
							</td>
							<td className="truncate">{s.base_url}</td>
							<td>{s.platform}</td>
							<td>
								<StatusBadge value={s.status} />
							</td>
							<td>{formatDate(s.updated_at)}</td>
							<td className="actions">
								<IconButton
									label={t("assets.editSite")}
									onClick={() => setEdit(s)}
								>
									<Pencil />
								</IconButton>
								<IconButton
									label={t("assets.deleteSite")}
									onClick={() => setRemove(s)}
								>
									<Trash2 />
								</IconButton>
							</td>
						</tr>
					))}
				</DataTable>
			)}
			{edit && (
				<SiteDialog
					value={edit}
					pending={save.isPending}
					error={save.error}
					onClose={() => setEdit(null)}
					onSave={(v) => save.mutate(v)}
				/>
			)}
			{remove && (
				<ConfirmDialog
					title={t("assets.deleteSite")}
					message={t("assets.deleteSiteMsg", { name: remove.name })}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove.id)}
				/>
			)}
		</Panel>
	);
}

function SiteDialog({
	value,
	pending,
	error,
	onClose,
	onSave,
}: {
	value: Partial<Site>;
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: Partial<Site>) => void;
}) {
	const { t } = useI18n();
	const [form, setForm] = useState(value);
	return (
		<Dialog
			title={value.id ? t("assets.editSite") : t("assets.addSite")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button disabled={pending} onClick={() => onSave(form)}>
						{t("common.save")}
					</Button>
				</>
			}
		>
			<div className="form-grid">
				<Field label={t("common.name")}>
					<input
						value={form.name ?? ""}
						onChange={(e) => setForm({ ...form, name: e.target.value })}
					/>
				</Field>
				<Field label={t("common.baseUrl")}>
					<input
						type="url"
						value={form.base_url ?? ""}
						onChange={(e) => setForm({ ...form, base_url: e.target.value })}
						placeholder="https://api.example.com"
					/>
					<small>{t("assets.siteBaseUrlHint")}</small>
				</Field>
				<Field label={t("common.platform")}>
					<select
						value={form.platform}
						onChange={(e) => setForm({ ...form, platform: e.target.value })}
					>
						<option>openai-compatible</option>
						<option>new-api</option>
						<option>one-api</option>
					</select>
				</Field>
				<Field label={t("common.status")}>
					<select
						value={form.status}
						onChange={(e) =>
							setForm({ ...form, status: e.target.value as Site["status"] })
						}
					>
						<option value="enabled">{t("common.enabled")}</option>
						<option value="disabled">{t("common.disabled")}</option>
					</select>
				</Field>
			</div>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function Credentials() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const qc = useQueryClient();
	const sites = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => service.sites(signal),
	});
	const [siteId, setSiteId] = useState(0);
	const selected = siteId || sites.data?.[0]?.id || 0;
	const creds = useQuery({
		queryKey: ["credentials", selected],
		queryFn: ({ signal }) => service.credentials(selected, signal),
		enabled: selected > 0,
	});
	const [add, setAdd] = useState(false);
	const [remove, setRemove] = useState<number | null>(null);
	const pendingCredential = useRef<{
		kind: string;
		secret: string;
		meta_json?: string;
		status: string;
	} | null>(null);
	const create = useMutation({
		mutationFn: async () => {
			const body = pendingCredential.current;
			if (!body) throw new Error("credential payload unavailable");
			try {
				return await service.createCredential(selected, body);
			} finally {
				pendingCredential.current = null;
			}
		},
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["credentials", selected] });
			setAdd(false);
		},
	});
	const del = useMutation({
		mutationFn: service.deleteCredential,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["credentials", selected] });
			setRemove(null);
		},
	});
	const toggle = useMutation({
		mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
			service.setCheckin(id, enabled),
		onSuccess: () =>
			qc.invalidateQueries({ queryKey: ["credentials", selected] }),
	});
	const run = useMutation({
		mutationFn: service.runCredential,
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["credentials", selected] });
			void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
		},
	});
	if (sites.isPending) return <Loading />;
	if (!sites.data?.length)
		return (
			<Panel>
				<Empty>{t("assets.needSite")}</Empty>
			</Panel>
		);
	return (
		<Panel
			actions={
				<>
					<select
						aria-label={t("common.site")}
						value={selected}
						onChange={(e) => setSiteId(Number(e.target.value))}
					>
						{sites.data.map((s) => (
							<option key={s.id} value={s.id}>
								{s.name}
							</option>
						))}
					</select>
					<Button icon={<Plus size={16} />} onClick={() => setAdd(true)}>
						{t("assets.addCredential")}
					</Button>
				</>
			}
		>
			{creds.isPending ? (
				<Loading />
			) : creds.isError ? (
				<ErrorState error={creds.error} />
			) : (
				<DataTable
					headers={[
						t("common.id"),
						t("common.kind"),
						t("common.secret"),
						t("common.status"),
						t("common.scheduled"),
						t("common.actions"),
					]}
					empty={!creds.data?.length}
				>
					{creds.data?.map((c) => (
						<tr key={c.id}>
							<td>#{c.id}</td>
							<td>
								<strong>{c.kind}</strong>
							</td>
							<td>{c.has_secret ? t("common.stored") : t("common.missing")}</td>
							<td>
								<StatusBadge value={c.status} />
							</td>
							<td>
								<label className="switch">
									<input
										type="checkbox"
										checked={c.checkin_enabled}
										onChange={(e) =>
											toggle.mutate({ id: c.id, enabled: e.target.checked })
										}
									/>
									<span />
								</label>
							</td>
							<td className="actions">
								<IconButton
									label={t("assets.runCheckin")}
									disabled={run.isPending && run.variables === c.id}
									onClick={() => run.mutate(c.id)}
								>
									<Play
										className={
											run.isPending && run.variables === c.id ? "spin" : ""
										}
									/>
								</IconButton>
								<IconButton
									label={t("assets.deleteCredential")}
									onClick={() => setRemove(c.id)}
								>
									<Trash2 />
								</IconButton>
							</td>
						</tr>
					))}
				</DataTable>
			)}
			{run.error && <ErrorState error={run.error} />}
			{run.data && (
				<div className="result-strip">
					<StatusBadge value={run.data.status} />
					<span>
						{t("assets.checkinResult", {
							id: run.data.credential_id,
							message: run.data.message,
							reward: run.data.reward ? ` · ${run.data.reward}` : "",
						})}
					</span>
				</div>
			)}
			{add && (
				<CredentialDialog
					pending={create.isPending}
					error={create.error}
					onClose={() => {
						pendingCredential.current = null;
						setAdd(false);
					}}
					onSave={(value) => {
						pendingCredential.current = value;
						create.mutate();
					}}
				/>
			)}
			{remove && (
				<ConfirmDialog
					title={t("assets.deleteCredential")}
					message={t("assets.deleteCredentialMsg", { id: remove })}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove)}
				/>
			)}
		</Panel>
	);
}

function CredentialDialog({
	pending,
	error,
	onClose,
	onSave,
}: {
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: {
		kind: string;
		secret: string;
		meta_json?: string;
		status: string;
	}) => void;
}) {
	const { t } = useI18n();
	const [kind, setKind] = useState("api_key");
	const [secret, setSecret] = useState("");
	const [meta, setMeta] = useState("");
	const submit = () => {
		onSave({ kind, secret, meta_json: meta, status: "enabled" });
		setSecret("");
	};
	return (
		<Dialog
			title={t("assets.addCredential")}
			onClose={() => {
				setSecret("");
				onClose();
			}}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button disabled={pending || !secret} onClick={submit}>
						{t("assets.storeCredential")}
					</Button>
				</>
			}
		>
			<Field label={t("common.kind")}>
				<select value={kind} onChange={(e) => setKind(e.target.value)}>
					<option value="api_key">{t("assets.kind.api_key")}</option>
					<option value="session">{t("assets.kind.session")}</option>
					<option value="access_token">{t("assets.kind.access_token")}</option>
					<option value="password">{t("assets.kind.password")}</option>
				</select>
			</Field>
			<Field label={t("common.secret")}>
				<input
					type="password"
					autoComplete="new-password"
					value={secret}
					onChange={(e) => setSecret(e.target.value)}
				/>
			</Field>
			<Field label={t("assets.metaJson")} hint={t("assets.secretMetaHint")}>
				<textarea value={meta} onChange={(e) => setMeta(e.target.value)} />
			</Field>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function Channels() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const qc = useQueryClient();
	const query = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => service.channels(signal),
	});
	const sites = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => service.sites(signal),
	});
	const [edit, setEdit] = useState<Partial<Channel> | null>(null);
	const [remove, setRemove] = useState<Channel | null>(null);
	const save = useMutation({
		mutationFn: (v: Partial<Channel>) =>
			v.id ? service.updateChannel(v.id, v) : service.createChannel(v),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["channels"] });
			setEdit(null);
		},
	});
	const del = useMutation({
		mutationFn: service.deleteChannel,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["channels"] });
			setRemove(null);
		},
	});
	const refresh = useMutation({
		mutationFn: service.refreshChannel,
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["channels"] });
			void qc.invalidateQueries({ queryKey: ["routes"] });
			void qc.invalidateQueries({ queryKey: ["models"] });
		},
	});
	if (sites.isPending) return <Loading />;
	if (!sites.data?.length)
		return (
			<Panel>
				<Empty>{t("assets.needSiteForChannel")}</Empty>
			</Panel>
		);
	return (
		<Panel
			actions={
				<Button
					icon={<Plus size={16} />}
					onClick={() =>
						setEdit({
							status: "enabled",
							group_name: "default",
							weight: 100,
							priority: 0,
							type_hint: "openai-compatible",
						})
					}
				>
					{t("assets.addChannel")}
				</Button>
			}
		>
			{query.isPending ? (
				<Loading />
			) : query.isError ? (
				<ErrorState error={query.error} />
			) : (
				<DataTable
					headers={[
						t("common.name"),
						t("common.endpoint"),
						t("common.models"),
						t("assets.priorityWeight"),
						t("common.status"),
						t("common.actions"),
					]}
					empty={!query.data.length}
				>
					{query.data.map((c) => (
						<tr key={c.id}>
							<td>
								<strong>{c.name}</strong>
								<small>#{c.id}</small>
							</td>
							<td className="truncate">{c.base_url}</td>
							<td className="truncate">{c.models_csv || "-"}</td>
							<td>
								{c.priority} / {c.weight}
							</td>
							<td>
								<StatusBadge value={c.status} />
							</td>
							<td className="actions">
								<IconButton
									label={t("assets.refreshDiscovery")}
									disabled={refresh.isPending && refresh.variables === c.id}
									onClick={() => refresh.mutate(c.id)}
								>
									<RefreshCw
										className={
											refresh.isPending && refresh.variables === c.id
												? "spin"
												: ""
										}
									/>
								</IconButton>
								<IconButton
									label={t("assets.editChannel")}
									onClick={() => setEdit(c)}
								>
									<Pencil />
								</IconButton>
								<IconButton
									label={t("assets.deleteChannel")}
									onClick={() => setRemove(c)}
								>
									<Trash2 />
								</IconButton>
							</td>
						</tr>
					))}
				</DataTable>
			)}
			{refresh.error && <ErrorState error={refresh.error} />}
			{refresh.data && (
				<div className="result-strip">
					<StatusBadge value="success" />
					<span>
						{t("assets.refreshResultChannel", {
							id: refresh.data.channel_id,
							models: refresh.data.models.length,
							routes: refresh.data.created_routes,
						})}
					</span>
				</div>
			)}
			{edit && (
				<ChannelDialog
					value={edit}
					sites={sites.data ?? []}
					pending={save.isPending}
					error={save.error}
					onClose={() => setEdit(null)}
					onSave={(v) => save.mutate(v)}
				/>
			)}
			{remove && (
				<ConfirmDialog
					title={t("assets.deleteChannel")}
					message={t("assets.deleteChannelMsg", { name: remove.name })}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove.id)}
				/>
			)}
		</Panel>
	);
}

function ChannelDialog({
	value,
	sites,
	pending,
	error,
	onClose,
	onSave,
}: {
	value: Partial<Channel>;
	sites: Site[];
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: Partial<Channel>) => void;
}) {
	const { t } = useI18n();
	const [f, setF] = useState(value);
	return (
		<Dialog
			title={value.id ? t("assets.editChannel") : t("assets.addChannel")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button disabled={pending || !f.name} onClick={() => onSave(f)}>
						{t("common.save")}
					</Button>
				</>
			}
		>
			<div className="form-grid">
				<Field label={t("common.name")}>
					<input
						value={f.name ?? ""}
						onChange={(e) => setF({ ...f, name: e.target.value })}
					/>
				</Field>
				<Field label={t("common.site")}>
					<select
						value={f.site_id ?? ""}
						onChange={(e) =>
							setF({ ...f, site_id: Number(e.target.value) || undefined })
						}
					>
						<option value="">{t("common.none")}</option>
						{sites.map((s) => (
							<option key={s.id} value={s.id}>
								{s.name}
							</option>
						))}
					</select>
				</Field>
				<Field label={t("assets.credentialId")}>
					<input
						type="number"
						min="1"
						value={f.credential_id ?? ""}
						onChange={(e) =>
							setF({ ...f, credential_id: Number(e.target.value) || undefined })
						}
					/>
				</Field>
				<Field label={t("common.baseUrl")}>
					<input
						type="url"
						value={f.base_url ?? ""}
						onChange={(e) => setF({ ...f, base_url: e.target.value })}
					/>
				</Field>
				<Field label={t("common.type")}>
					<select
						value={f.type_hint}
						onChange={(e) => setF({ ...f, type_hint: e.target.value })}
					>
						<option>openai-compatible</option>
						<option>new-api</option>
					</select>
				</Field>
				<Field label={t("assets.modelsCsv")}>
					<input
						value={f.models_csv ?? ""}
						onChange={(e) => setF({ ...f, models_csv: e.target.value })}
					/>
				</Field>
				<Field label={t("common.group")}>
					<input
						value={f.group_name ?? ""}
						onChange={(e) => setF({ ...f, group_name: e.target.value })}
					/>
				</Field>
				<Field label={t("common.priority")}>
					<input
						type="number"
						value={f.priority ?? 0}
						onChange={(e) => setF({ ...f, priority: Number(e.target.value) })}
					/>
				</Field>
				<Field label={t("common.weight")}>
					<input
						type="number"
						min="0"
						value={f.weight ?? 0}
						onChange={(e) => setF({ ...f, weight: Number(e.target.value) })}
					/>
				</Field>
				<Field label={t("common.status")}>
					<select
						value={f.status}
						onChange={(e) =>
							setF({ ...f, status: e.target.value as Channel["status"] })
						}
					>
						<option value="enabled">{t("common.enabled")}</option>
						<option value="disabled">{t("common.disabled")}</option>
					</select>
				</Field>
			</div>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function Keys() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const qc = useQueryClient();
	const query = useQuery({
		queryKey: ["keys"],
		queryFn: ({ signal }) => service.keys(signal),
	});
	const [add, setAdd] = useState(false);
	const [created, setCreated] = useState<CreatedDownstreamKey | null>(null);
	const [remove, setRemove] = useState<number | null>(null);
	const create = useMutation({
		mutationFn: async (v: { name: string; scopes: string }) => {
			const result = await service.createKey(v);
			setCreated(result);
		},
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["keys"] });
			setAdd(false);
		},
	});
	const del = useMutation({
		mutationFn: service.deleteKey,
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["keys"] });
			setRemove(null);
		},
	});
	return (
		<Panel
			actions={
				<Button icon={<Plus size={16} />} onClick={() => setAdd(true)}>
					{t("assets.createKey")}
				</Button>
			}
		>
			{query.isPending ? (
				<Loading />
			) : query.isError ? (
				<ErrorState error={query.error} />
			) : (
				<DataTable
					headers={[
						t("common.name"),
						t("common.scopes"),
						t("common.status"),
						t("common.created"),
						t("common.actions"),
					]}
					empty={!query.data.length}
				>
					{query.data.map((k) => (
						<tr key={k.id}>
							<td>
								<strong>{k.name}</strong>
								<small>#{k.id}</small>
							</td>
							<td>{k.scopes || "relay"}</td>
							<td>
								<StatusBadge value={k.enabled} />
							</td>
							<td>{formatDate(k.created_at)}</td>
							<td className="actions">
								<IconButton
									label={t("assets.deleteKey")}
									onClick={() => setRemove(k.id)}
								>
									<Trash2 />
								</IconButton>
							</td>
						</tr>
					))}
				</DataTable>
			)}
			{add && (
				<KeyDialog
					pending={create.isPending}
					error={create.error}
					onClose={() => setAdd(false)}
					onSave={(v) => create.mutate(v)}
				/>
			)}
			{created && (
				<Dialog
					title={t("assets.copyKeyTitle")}
					onClose={() => setCreated(null)}
					actions={
						<Button onClick={() => setCreated(null)}>
							{t("assets.storedKey")}
						</Button>
					}
				>
					<p className="warning">{t("assets.copyKeyWarning")}</p>
					<div className="secret-output">
						<code>{created.token}</code>
						<IconButton
							label={t("assets.copyToken")}
							onClick={() => navigator.clipboard.writeText(created.token)}
						>
							<Copy />
						</IconButton>
					</div>
				</Dialog>
			)}
			{remove && (
				<ConfirmDialog
					title={t("assets.revokeKey")}
					message={t("assets.revokeKeyMsg")}
					confirmLabel={t("assets.revokeKeyConfirm")}
					pending={del.isPending}
					error={del.error}
					onClose={() => setRemove(null)}
					onConfirm={() => del.mutate(remove)}
				/>
			)}
		</Panel>
	);
}

function KeyDialog({
	pending,
	error,
	onClose,
	onSave,
}: {
	pending: boolean;
	error: unknown;
	onClose: () => void;
	onSave: (v: { name: string; scopes: string }) => void;
}) {
	const { t } = useI18n();
	const [name, setName] = useState("");
	const [scopes, setScopes] = useState("relay");
	return (
		<Dialog
			title={t("assets.createDownstreamKey")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={pending || !name.trim()}
						icon={<KeyRound size={16} />}
						onClick={() => onSave({ name, scopes })}
					>
						{t("common.create")}
					</Button>
				</>
			}
		>
			<Field label={t("common.name")}>
				<input
					autoFocus
					value={name}
					onChange={(e) => setName(e.target.value)}
				/>
			</Field>
			<Field label={t("common.scopes")}>
				<input value={scopes} onChange={(e) => setScopes(e.target.value)} />
			</Field>
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}
