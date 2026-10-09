import { Megaphone, RefreshCw } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { SiteAnnouncement } from "../api/types";
import { Button, ErrorState, Loading, Panel } from "../components/ui";
import { useI18n } from "../i18n";
import { relativeTime } from "../lib/format";
import { useSession } from "../session";

/** How long a feed stays open before it is marked as read. */
const SEEN_AFTER_MS = 5_000;
/** Rows shown at once; the rest are behind the same scroll as the logs panel. */
const MAX_ROWS = 12;
/** How many rows the API is asked for: enough to survive a burst of notices. */
const FETCH_ROWS = 60;

const SEEN_KEY = "meta-gateway.site-news-seen";

/**
 * 上游站点消息：what each upstream site publishes on its own notice board.
 *
 * The gateway reads the boards on a cadence (internal/sitenews), so this panel
 * renders stored rows — opening the overview never waits on twenty-odd sites —
 * and the refresh button is the only request that goes out to them.
 *
 * "New" is measured against the last visit on this browser, not against the
 * site's clock: an announcement first read here is new to the operator whatever
 * the site called its publish date. The stamp moves a few seconds after the
 * feed renders, so the badges are visible on the visit that earned them and
 * gone on the next one.
 */
export function SiteNewsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [openRows, setOpenRows] = useState<Record<number, boolean>>({});
  const [seenAt, setSeenAt] = useState(() => readSeen());
  const [notice, setNotice] = useState<string | null>(null);

  const feed = useQuery({
    queryKey: ["site-news", client],
    queryFn: ({ signal }) => api(client!).siteNews(FETCH_ROWS, signal),
    refetchInterval: 120_000,
  });
  // A plain mutation, not the admin helper: this panel reports the round in its
  // own line ("read 22 sites, 3 new"), so a toast would say it twice — and the
  // overview renders without a toast provider in several tests.
  const refresh = useMutation({
    mutationFn: () => api(client!).refreshSiteNews(),
    onSuccess: (result) => {
      setNotice(
        result.failed > 0
          ? t("dashboard.siteNews.refreshedPartial", {
              sites: String(result.fetched),
              added: String(result.added),
              failed: String(result.failed),
            })
          : t("dashboard.siteNews.refreshed", {
              sites: String(result.fetched),
              added: String(result.added),
            }),
      );
      void queryClient.invalidateQueries({ queryKey: ["site-news"] });
    },
  });

  const items = feed.data?.items ?? [];
  const fetchedAt = feed.dataUpdatedAt;
  useEffect(() => {
    if (items.length === 0) return;
    const stamp = new Date().toISOString();
    const timer = window.setTimeout(() => {
      writeSeen(stamp);
      setSeenAt(stamp);
    }, SEEN_AFTER_MS);
    return () => window.clearTimeout(timer);
  }, [items.length, fetchedAt]);

  const seenTime = seenAt ? new Date(seenAt).getTime() : 0;
  const isNew = (item: SiteAnnouncement) => new Date(item.first_seen_at).getTime() > seenTime;
  const coverage = feed.data?.sites;

  return (
    <Panel className="cockpit-panel cockpit-news-panel">
      <div className="panel-header">
        <div className="cockpit-panel-title">
          <Megaphone size={14} />
          <strong>{t("dashboard.siteNews")}</strong>
        </div>
        <div className="site-news-actions">
          {coverage ? (
            <span className="panel-muted">
              {t("dashboard.siteNews.coverage", {
                readable: String(coverage.readable),
                reported: String(coverage.reported),
              })}
            </span>
          ) : null}
          <Button
            variant="quiet"
            icon={<RefreshCw size={14} className={refresh.isPending ? "spin" : ""} />}
            disabled={refresh.isPending}
            onClick={() => {
              setNotice(null);
              refresh.mutate(undefined);
            }}
          >
            {t("dashboard.siteNews.refresh")}
          </Button>
        </div>
      </div>

      {notice ? <p className="site-news-notice">{notice}</p> : null}
      {feed.isPending ? <Loading /> : null}
      {feed.error ? <ErrorState error={feed.error} /> : null}
      {feed.data && items.length === 0 ? (
        <p className="dashboard-empty">{t("dashboard.siteNews.empty")}</p>
      ) : null}

      {items.length > 0 ? (
        <ul className="site-news-list">
          {items.slice(0, MAX_ROWS).map((item) => {
            const fresh = isNew(item);
            return (
              <li key={item.id} className={`site-news-item${fresh ? " is-new" : ""}`}>
                <div className="site-news-head">
                  <span className="site-news-site">{item.site_name || `#${item.site_id}`}</span>
                  {fresh ? (
                    <span className="site-news-badge">{t("dashboard.siteNews.new")}</span>
                  ) : null}
                  <span className="site-news-time">{relativeTime(item.published_at, t)}</span>
                </div>
                <p
                  className={`site-news-text${openRows[item.id] ? " is-open" : ""}`}
                  onClick={() => setOpenRows((map) => ({ ...map, [item.id]: !map[item.id] }))}
                  title={t("dashboard.siteNews.expandHint")}
                >
                  {item.content}
                </p>
                {item.extra ? (
                  <a className="site-news-link" href={item.extra} target="_blank" rel="noreferrer">
                    {t("dashboard.siteNews.details")}
                  </a>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}

      <p className="panel-muted site-news-foot">{t("dashboard.siteNews.sourceHint")}</p>
    </Panel>
  );
}

function readSeen(): string {
  try {
    return localStorage.getItem(SEEN_KEY) ?? "";
  } catch {
    return "";
  }
}

function writeSeen(value: string) {
  try {
    localStorage.setItem(SEEN_KEY, value);
  } catch {
    /* A browser that refuses storage still gets the feed, just without badges. */
  }
}
