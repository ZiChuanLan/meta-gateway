import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Field, Panel } from "../components/ui";
import { teamText, teamError } from "./text";
import type { ModeInfo, TeamRequest } from "./types";
import "./team.css";

/**
 * Personal / team mode: the switch that turns the whole multi-user module on.
 *
 * It owns its own endpoint rather than riding along in a settings draft, and it
 * is the only control on the module's overview board — turning the mode on is
 * what makes the other boards (members, policies, quotas…) exist at all. The
 * card anatomy stays generic (`Panel` + `panel-header` + `Field`) so it renders
 * inside whichever shell hosts it.
 */
export function ModePanel({
  request,
  locale,
  className = "runtime-card runtime-card-mode",
}: {
  request: TeamRequest;
  locale: string;
  /** Container class; the runtime settings page and the module's own overview
   *  board each pass their own so neither inherits the other's card style. */
  className?: string;
}) {
  const t = teamText(locale);
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: ["team", "mode"],
    queryFn: ({ signal }) => request<ModeInfo>("/admin/mode", { signal }),
  });
  const saving = useRef(false);
  const [choice, setChoice] = useState<"personal" | "team" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function save(path: string, method: string, body: unknown) {
    if (saving.current) return;
    saving.current = true;
    setBusy(true);
    setError(null);
    try {
      await request(path, { method, body: JSON.stringify(body) });
      setChoice(null);
      await qc.invalidateQueries({ queryKey: ["team"] });
    } catch (e) {
      setError(e);
    } finally {
      saving.current = false;
      setBusy(false);
    }
  }

  const mode = choice ?? query.data?.mode ?? "personal";
  const owner = query.data?.role === "owner";

  return (
    <Panel className={className} id="runtime-mode">
      <div className="panel-header">
        <strong>{t("currentMode")}</strong>
        {query.data?.mode === "team" && (
          <a className="button button-quiet" href="/console/users/members">
            {t("team")} →
          </a>
        )}
      </div>
      {Boolean(error || query.error) && (
        <div role="alert" className="team-error">
          {teamError(error || query.error, locale)}{" "}
          <Button variant="quiet" onClick={() => void query.refetch()}>
            {t("retry")}
          </Button>
        </div>
      )}
      {query.isPending ? (
        <p className="muted panel-lede">{t("load")}</p>
      ) : query.data ? (
        <>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              if (
                mode === "personal" &&
                query.data.mode === "team" &&
                !confirm(t("disableHint"))
              )
                return;
              void save("/admin/mode", "PATCH", { mode });
            }}
          >
            <fieldset className="team-modal-content" disabled={busy || !owner}>
              <select
                aria-label={t("mode")}
                className="runtime-mode-select"
                value={mode}
                onChange={(e) =>
                  setChoice(e.target.value as "personal" | "team")
                }
              >
                <option value="personal">{t("personal")}</option>
                <option value="team" disabled={!query.data.has_owner}>
                  {t("enabled")}
                </option>
              </select>
              <p className="muted panel-lede" style={{ marginTop: 12 }}>
                {t("disableHint")}
              </p>
              <Button
                type="submit"
                style={{ marginTop: 6 }}
                disabled={mode === query.data.mode}
              >
                {busy ? t("saving") : t("save")}
              </Button>
            </fieldset>
          </form>
          {!query.data.has_owner && owner && (
            <form
              style={{ marginTop: 22 }}
              onSubmit={(event) => {
                event.preventDefault();
                const data = new FormData(event.currentTarget);
                void save("/admin/mode/owner", "POST", {
                  username: data.get("username"),
                  name: data.get("name"),
                  password: data.get("password"),
                  totp: data.get("totp"),
                });
              }}
            >
              <h4 style={{ margin: "0 0 6px", fontSize: 13 }}>
                {t("bootstrap")}
              </h4>
              <p className="muted panel-lede">{t("bootstrapHint")}</p>
              <fieldset className="team-modal-content" disabled={busy}>
                <div className="form-grid">
                  <Field label={t("username")}>
                    <input
                      name="username"
                      minLength={3}
                      maxLength={64}
                      required
                      autoComplete="username"
                    />
                  </Field>
                  <Field label={t("name")}>
                    <input name="name" maxLength={80} required />
                  </Field>
                  <Field label={`${t("password")} · ${t("passwordHint")}`}>
                    <input
                      name="password"
                      type="password"
                      minLength={10}
                      required
                      autoComplete="new-password"
                    />
                  </Field>
                  <Field label={t("totp")}>
                    <input
                      name="totp"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                    />
                  </Field>
                </div>
                <Button type="submit" variant="secondary">
                  {busy ? t("saving") : t("bootstrap")}
                </Button>
              </fieldset>
            </form>
          )}
        </>
      ) : null}
    </Panel>
  );
}
