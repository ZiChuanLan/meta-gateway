import { Activity, KeyRound, Route as RouteIcon, Server } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
	DataTable,
	Empty,
	ErrorState,
	Loading,
	Page,
	Panel,
	StatusBadge,
	formatDate,
} from "../components/ui";

export function Dashboard() {
	const { client } = useSession();
	const { t } = useI18n();
	const service = api(client!);
	const sites = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => service.sites(signal),
	});
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => service.channels(signal),
	});
	const routes = useQuery({
		queryKey: ["routes"],
		queryFn: ({ signal }) => service.routes(signal),
	});
	const keys = useQuery({
		queryKey: ["keys"],
		queryFn: ({ signal }) => service.keys(signal),
	});
	const proxy = useQuery({
		queryKey: ["proxy-logs"],
		queryFn: ({ signal }) => service.proxyLogs(signal),
	});
	const checkins = useQuery({
		queryKey: ["checkin-logs", "dashboard"],
		queryFn: ({ signal }) => service.checkinLogs("?limit=5", signal),
	});
	const audit = useQuery({
		queryKey: ["audit", undefined],
		queryFn: ({ signal }) => service.auditEvents(undefined, signal),
	});
	const core = [sites, channels, routes, keys];
	return (
		<Page title={t("dashboard.title")} description={t("dashboard.description")}>
			<div className="stat-grid">
				<Stat
					icon={<Server />}
					label={t("dashboard.sites")}
					value={sites.data?.length}
				/>
				<Stat
					icon={<Activity />}
					label={t("dashboard.channels")}
					value={channels.data?.length}
				/>
				<Stat
					icon={<RouteIcon />}
					label={t("dashboard.routes")}
					value={routes.data?.length}
				/>
				<Stat
					icon={<KeyRound />}
					label={t("dashboard.keys")}
					value={keys.data?.length}
				/>
			</div>
			{core.some((q) => q.isPending) && <Loading />}
			{core.some((q) => q.isError) && (
				<ErrorState error={core.find((q) => q.error)?.error} />
			)}
			<div className="dashboard-grid">
				<Panel title={t("dashboard.proxy")}>
					{proxy.isPending ? (
						<Loading />
					) : proxy.isError ? (
						<ErrorState error={proxy.error} />
					) : (
						<DataTable
							headers={[
								t("common.model"),
								t("common.channel"),
								t("common.status"),
								t("common.latency"),
								t("common.time"),
							]}
							empty={!proxy.data?.length}
						>
							{proxy.data?.slice(0, 6).map((log) => (
								<tr key={log.id}>
									<td>
										<strong>{log.model}</strong>
										<small>{t("common.attempt", { n: log.attempt })}</small>
									</td>
									<td>#{log.channel_id}</td>
									<td>
										<StatusBadge value={String(log.status)} />
									</td>
									<td>{t("common.ms", { n: log.latency_ms })}</td>
									<td>{formatDate(log.created_at)}</td>
								</tr>
							))}
						</DataTable>
					)}
				</Panel>
				<Panel title={t("dashboard.checkins")}>
					{checkins.isPending ? (
						<Loading />
					) : checkins.isError ? (
						<ErrorState error={checkins.error} />
					) : !checkins.data?.length ? (
						<Empty />
					) : (
						<div className="activity-list">
							{checkins.data.map((log) => (
								<div key={log.id}>
									<StatusBadge value={log.status} />
									<div>
										<strong>
											{t("common.credential", { id: log.credential_id })}
										</strong>
										<span>
											{t("common.dotJoin", {
												a: log.category,
												b: formatDate(log.ran_at),
											})}
										</span>
									</div>
								</div>
							))}
						</div>
					)}
				</Panel>
				<Panel title={t("dashboard.audit")} className="span-two">
					{audit.isPending ? (
						<Loading />
					) : audit.isError ? (
						<ErrorState error={audit.error} />
					) : (
						<DataTable
							headers={[
								t("common.action"),
								t("common.resource"),
								t("common.outcome"),
								t("common.status"),
								t("common.time"),
							]}
							empty={!audit.data?.length}
						>
							{audit.data?.slice(0, 8).map((event) => (
								<tr key={event.id}>
									<td>
										<strong>{event.action}</strong>
									</td>
									<td>
										{event.resource_kind || "-"}
										{event.resource_id ? ` #${event.resource_id}` : ""}
									</td>
									<td>
										<StatusBadge value={event.outcome} />
									</td>
									<td>{event.status_code}</td>
									<td>{formatDate(event.created_at)}</td>
								</tr>
							))}
						</DataTable>
					)}
				</Panel>
			</div>
		</Page>
	);
}

function Stat({
	icon,
	label,
	value,
}: {
	icon: React.ReactNode;
	label: string;
	value?: number;
}) {
	return (
		<div className="stat">
			<span aria-hidden="true">{icon}</span>
			<div>
				<small>{label}</small>
				<strong>{value ?? "-"}</strong>
			</div>
		</div>
	);
}
