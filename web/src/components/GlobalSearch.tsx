import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, X } from "lucide-react";
import { api } from "../api/client";
import type { SearchHits } from "../api/types";
import { useSession } from "../session";
import { useI18n } from "../i18n";

// GlobalSearch is the sidebar search box: debounced query against the grouped
// /admin/search endpoint with a grouped dropdown (channels / models / keys /
// logs). Selecting a hit navigates to the relevant page with its filter set.
export function GlobalSearch() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const navigate = useNavigate();
  const [term, setTerm] = useState("");
  const [debounced, setDebounced] = useState("");
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handle = setTimeout(() => setDebounced(term.trim()), 250);
    return () => clearTimeout(handle);
  }, [term]);

  useEffect(() => {
    setOpen(debounced.length > 0);
  }, [debounced]);

  useEffect(() => {
    const onDocClick = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, []);

  const query = useQuery({
    queryKey: ["global-search", debounced],
    queryFn: ({ signal }) => service.globalSearch(debounced, signal),
    enabled: debounced.length > 0,
    placeholderData: (prev) => prev,
  });

  const hits: SearchHits = query.data ?? {
    channels: [],
    routes: [],
    credentials: [],
    logs: [],
  };
  const total =
    hits.channels.length +
    hits.routes.length +
    hits.credentials.length +
    hits.logs.length;

  const go = (to: string) => {
    setTerm("");
    setDebounced("");
    setOpen(false);
    navigate(to);
  };

  const groups: { key: string; title: string; render: () => React.ReactNode }[] =
    [
      {
        key: "channels",
        title: t("search.channels"),
        render: () => (
          <>
            {hits.channels.map((c) => (
              <button
                key={"c" + c.id}
                className="search-hit"
                onClick={() => go(`/channels?search=${encodeURIComponent(c.name)}`)}
              >
                <span className="search-hit-name">{c.name}</span>
                <span className="search-hit-meta">{c.url}</span>
              </button>
            ))}
          </>
        ),
      },
      {
        key: "routes",
        title: t("search.models"),
        render: () => (
          <>
            {hits.routes.map((r) => (
              <button
                key={"r" + r.id}
                className="search-hit"
                onClick={() => go(`/models?model=${encodeURIComponent(r.model)}`)}
              >
                <span className="search-hit-name mono">{r.model}</span>
                <span
                  className={
                    "search-hit-meta " + (r.status === "enabled" ? "is-ok" : "is-off")
                  }
                >
                  {r.status}
                </span>
              </button>
            ))}
          </>
        ),
      },
      {
        key: "keys",
        title: t("search.keys"),
        render: () => (
          <>
            {hits.credentials.map((k) => (
              <button
                key={"k" + k.id}
                className="search-hit"
                onClick={() => go(`/keys?search=${encodeURIComponent(k.name)}`)}
              >
                <span className="search-hit-name">{k.name}</span>
                <span className="search-hit-meta">{k.kind}</span>
              </button>
            ))}
          </>
        ),
      },
      {
        key: "logs",
        title: t("search.logs"),
        render: () => (
          <>
            {hits.logs.map((l) => (
              <button
                key={"l" + l.id}
                className="search-hit"
                onClick={() =>
                  go(
                    `/logs?upstream_request_id=${encodeURIComponent(
                      l.upstream_request_id || l.request_id,
                    )}`,
                  )
                }
              >
                <span className="search-hit-name mono">{l.model || l.request_id}</span>
                <span className="search-hit-meta">{l.request_id.slice(0, 24)}</span>
              </button>
            ))}
          </>
        ),
      },
    ].filter((g) => g.render().props.children.length > 0);

  return (
    <div className="global-search" ref={boxRef}>
      <div className="global-search-box">
        <Search size={14} className="global-search-icon" />
        <input
          value={term}
          placeholder={t("search.placeholder")}
          onChange={(e) => {
            setTerm(e.target.value);
            setOpen(true);
          }}
          onFocus={() => debounced && setOpen(true)}
        />
        {term ? (
          <button
            type="button"
            className="global-search-clear"
            onClick={() => {
              setTerm("");
              setDebounced("");
              setOpen(false);
            }}
          >
            <X size={12} />
          </button>
        ) : null}
      </div>
      {open && debounced ? (
        <div className="search-dropdown">
          {query.isFetching && !query.data ? (
            <div className="search-status">{t("search.searching")}</div>
          ) : total === 0 ? (
            <div className="search-status">{t("search.empty")}</div>
          ) : (
            groups.map((g) => (
              <section key={g.key} className="search-group">
                <h4>{g.title}</h4>
                {g.render()}
              </section>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}
