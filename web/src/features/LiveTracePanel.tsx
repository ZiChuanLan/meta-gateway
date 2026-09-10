import { Ban, CircleDot, Radio, WifiOff } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "../api/client";
import type { LiveTraceRequest } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { DataTable, Panel, Button } from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { useToast } from "../toast";

// Console-side live view of in-flight relay requests. Subscribes to the
// admin SSE stream (/admin/relay/live) while mounted, merges frames by
// request ID (the backend pushes a snapshot as the first events, then
// deltas), and keeps a bounded window of settled requests. EventSource
// cannot set an Authorization header, so we stream raw fetch ourselves.

type ConnState = "connecting" | "live" | "reconnecting" | "offline";

const RETAIN_SETTLED = 40; // front-end cap for settled requests
const BASE_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 15000;

function parseLiveFrames(raw: string): LiveTraceRequest[] {
  const frames: LiveTraceRequest[] = [];
  // SSE frames arrive as "event: request\ndata: {...}\n\n". Comments
  // (": keepalive" heartbeats) and unknown event types are ignored.
  for (const block of raw.split(/\r?\n\r?\n/)) {
    let eventName = "";
    let dataLine = "";
    for (const line of block.split(/\r?\n/)) {
      if (line.startsWith(":")) continue;
      if (line.startsWith("event:")) eventName = line.slice(6).trim();
      else if (line.startsWith("data:")) {
        dataLine = (dataLine ? dataLine + "\n" : "") + line.slice(5).trimStart();
      }
    }
    if (eventName !== "request" || !dataLine) continue;
    try {
      const frame = JSON.parse(dataLine) as LiveTraceRequest;
      if (frame && typeof frame.request_id === "string") frames.push(frame);
    } catch {
      // Ignore a malformed frame; the stream stays usable.
    }
  }
  return frames;
}

export { parseLiveFrames };

/** True when the frame ends the request lifecycle. */
function isSettled(frame: LiveTraceRequest) {
  return frame.status !== "running";
}

function LiveTraceRow({
  frame,
  channelNames,
  onInterrupt,
}: {
  frame: LiveTraceRequest;
  channelNames: Map<number, string>;
  onInterrupt: () => void;
}) {
  const { t } = useI18n();
  const channel = channelNames.get(Number(frame.target_channel));
  const target =
    frame.target_channel != null
      ? Number.isFinite(Number(frame.target_channel))
        ? (channel ?? `#${frame.target_channel}`)
        : "—"
      : "—";
  return (
    <tr className={frame.status !== "running" ? "live-trace-settled" : undefined}>
      <td>
        <span className="live-trace-model">
          <strong>{frame.model}</strong>
          <small className="mono">{frame.request_id}</small>
        </span>
      </td>
      <td>{target}</td>
      <td>
        <code className="mono">{String(frame.round)}</code>
      </td>
      <td>
        {frame.status === "running" ? (
          <span className="live-trace-now" aria-hidden="true" />
        ) : null}
        <span className={`live-trace-status is-${frame.status}`}>
          <span className="live-trace-pulse" aria-hidden="true" />
          {t(`status.${frame.status}`)}
        </span>
        {frame.status === "running" && frame.key_name ? (
          <small className="live-trace-key mono" title={t("logsLive.keyUsed")}>
            {frame.key_name}
          </small>
        ) : null}
        {frame.error ? (
          <span className="live-trace-error" title={frame.error}>
            {frame.error}
          </span>
        ) : null}
      </td>
      <td className="live-trace-duration">
        {frame.status === "running"
          ? Number(frame.duration_ms) > 0
            ? t("common.ms", { n: frame.duration_ms })
            : "—"
          : t("common.ms", { n: frame.duration_ms })}
      </td>
      <td className="live-trace-action">
        {frame.status === "running" ? (
          <Button
            variant="quiet"
            icon={<Ban size={14} />}
            onClick={onInterrupt}
            title={t("logsLive.interrupt")}
            aria-label={t("logsLive.interrupt")}
          >
            {t("logsLive.interrupt")}
          </Button>
        ) : (
          <span className="live-trace-terminal" aria-hidden="true">
            ·
          </span>
        )}
      </td>
    </tr>
  );
}

function LiveTraceEmpty() {
  const { t } = useI18n();
  return (
    <div className="live-trace-empty">
      <EmptyHero
        kicker="SSE · /admin/relay/live"
        title={t("logsLive.emptyTitle")}
        body={t("logsLive.emptyBody")}
      />
    </div>
  );
}

/**
 * Live relay traffic panel. Subscribes on mount, reconnects with backoff on
 * stream loss, and keeps the most recent frames per request ID.
 */
export function LiveTracePanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const [conn, setConn] = useState<ConnState>("connecting");
  const [frames, setFrames] = useState<LiveTraceRequest[]>([]);
  const [interrupting, setInterrupting] = useState<Set<string>>(
    () => new Set(),
  );
	const channelNames = useRef<Map<number, string>>(new Map());

  // Channel name resolution: keep a lightweight map for nicer labels.
  useEffect(() => {
    if (!client) return;
    let cancelled = false;
    api(client)
      .channels()
      .then((channels) => {
        if (cancelled) return;
        channels.forEach((c) => channelNames.current.set(c.id, c.name));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [client]);

  // SSE lifecycle: connect → read frames → merge; backoff reconnect on drop.
  useEffect(() => {
    if (!client) return;
    let disposed = false;
    let backoff = BASE_BACKOFF_MS;
    let retryTimer = 0;
    let abort: AbortController | null = null;

    const connect = async () => {
      abort = new AbortController();
      try {
        setConn((c) => (c === "live" ? "reconnecting" : c));
        const res = await client.openLiveTrace(abort.signal);
        const decoder = new TextDecoder();
        let buffer = "";
        setConn("live");
        backoff = BASE_BACKOFF_MS;

        const reader = res.body?.getReader();
        if (!reader) throw new Error("unreadable stream");
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (disposed) return;
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            // Only split on complete double-newline boundaries; keep the
            // trailing partial frame in the buffer.
            const chunks = buffer.split("\n\n");
            buffer = chunks.pop() ?? "";
            if (chunks.length === 0) continue;
            const parsed = parseLiveFrames(chunks.join("\n\n"));
            if (parsed.length === 0) continue;
            mergeFrames(parsed);
          }
        } finally {
          reader.releaseLock();
        }
        // A clean stream close without disposal = server side terminated.
        if (!disposed) scheduleReconnect();
      } catch (err) {
        if (disposed) return;
        if (err instanceof ApiError && err.status === 401) {
          setConn("offline");
          return;
        }
        scheduleReconnect();
      }
    };

    const scheduleReconnect = () => {
      if (disposed) return;
      setConn((prev) => (prev === "live" ? "reconnecting" : "connecting"));
      retryTimer = window.setTimeout(() => {
        if (!disposed) void connect();
      }, backoff);
      backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
    };

	void connect();
	return () => {
		disposed = true;
		window.clearTimeout(retryTimer);
		abort?.abort();
	};
	}, [client]);

  const mergeFrames = (incoming: LiveTraceRequest[]) => {
    setFrames((current) => {
      const byId = new Map<string, LiveTraceRequest>();
      // Newest snapshot frames come first from the backend; preserve the
      // order of first appearance so newest stay on top.
      const order: string[] = [];
      const settledCount = current.filter(isSettled).length;
      let settledToDrop =
        settledCount + incoming.filter(isSettled).length - RETAIN_SETTLED;
      for (const frame of incoming) {
        if (!byId.has(frame.request_id)) {
          order.push(frame.request_id);
          byId.set(frame.request_id, frame);
        }
      }
      for (const frame of current) {
        if (!byId.has(frame.request_id)) {
          order.push(frame.request_id);
          byId.set(frame.request_id, frame);
        }
      }
      // Drop oldest settled frames beyond the cap, starting from the tail
      // (oldest first? — order is newest first, so drop from the end).
      const visible: LiveTraceRequest[] = [];
      for (const id of order) {
        const frame = byId.get(id)!;
        if (isSettled(frame) && settledToDrop > 0) {
          settledToDrop -= 1;
          continue;
        }
        visible.push(frame);
      }
      return visible;
    });
  };

  const runningCount = frames.filter((f) => f.status === "running").length;

	const interrupt = (requestId: string) => {
		if (!client) return;
		setInterrupting((prev) => new Set(prev).add(requestId));
		client
			.interruptLiveRequest(requestId)
      .then(() => {
        toast.push({
          tone: "success",
          message: t("logsLive.interrupted", { id: requestId }),
        });
      })
      .catch((err: unknown) => {
        const message =
          err instanceof ApiError && err.status === 404
            ? t("logsLive.notInFlight")
            : t("logsLive.interruptFailed");
        toast.pushError(new Error(message));
      })
      .finally(() => {
        setInterrupting(
          (prev) => new Set([...prev].filter((id) => id !== requestId)),
        );
      });
  };

  const connMeta: Record<ConnState, { icon: ReactNode; label: string; className: string }> = {
    connecting: {
      icon: <Radio className="spin" size={12} />,
      label: t("logsLive.connecting"),
      className: "is-warn",
    },
    live: {
      icon: <CircleDot size={12} />,
      label: t("logsLive.live"),
      className: "is-ok",
    },
    reconnecting: {
      icon: <Radio className="spin" size={12} />,
      label: t("logsLive.reconnecting"),
      className: "is-warn",
    },
    offline: {
      icon: <WifiOff size={12} />,
      label: t("logsLive.offline"),
      className: "is-bad",
    },
  };
  const state = connMeta[conn];

  const showEmpty = frames.length === 0 && conn !== "connecting";

  return (
    <div className="live-trace-panel">
      <div className="toolbar live-trace-toolbar">
        <div className="live-trace-stats">
          <span className={`live-trace-state ${state.className}`}>
            {state.icon}
            {state.label}
          </span>
          <span className="live-trace-count">
            {t("logsLive.inFlight", { n: runningCount })}
          </span>
          <span className="live-trace-count is-quiet">
            {t("logsLive.total", { n: frames.length })}
          </span>
        </div>
        {interrupting.size > 0 ? (
          <span className="live-trace-interrupting is-quiet">
            {t("logsLive.interrupting", { n: interrupting.size })}
          </span>
        ) : null}
        <span className="live-trace-hint is-quiet">
          {t("logsLive.hint")}
        </span>
      </div>
      <Panel className="ops-list-panel">
        {showEmpty ? (
          <LiveTraceEmpty />
        ) : (
          <DataTable
            headers={[
              t("common.model"),
              t("logsLive.channel"),
              t("logsLive.round"),
              t("common.status"),
              t("common.latency"),
              t("logsLive.actions"),
            ]}
            empty={frames.length === 0}
          >
            {frames.map((frame) => (
              <LiveTraceRow
                key={frame.request_id}
                frame={frame}
                channelNames={channelNames.current}
                onInterrupt={() => interrupt(frame.request_id)}
              />
            ))}
          </DataTable>
        )}
      </Panel>
    </div>
  );
}