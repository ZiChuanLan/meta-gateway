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

type Tab = "Sites" | "Credentials" | "Channels" | "Downstream Keys";
export function Assets() {
  const [tab, setTab] = useState<Tab>("Sites");
  return (
    <Page
      title="Assets"
      description="Manage upstream providers, credentials, relay channels, and client access keys."
    >
      <Tabs
        items={["Sites", "Credentials", "Channels", "Downstream Keys"]}
        active={tab}
        onChange={(v) => setTab(v as Tab)}
      />
      {tab === "Sites" && <Sites />}
      {tab === "Credentials" && <Credentials />}
      {tab === "Channels" && <Channels />}
      {tab === "Downstream Keys" && <Keys />}
    </Page>
  );
}

function Sites() {
  const { client } = useSession();
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
          Add site
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
            "Name",
            "Base URL",
            "Platform",
            "Status",
            "Updated",
            "Actions",
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
                <IconButton label="Edit site" onClick={() => setEdit(s)}>
                  <Pencil />
                </IconButton>
                <IconButton label="Delete site" onClick={() => setRemove(s)}>
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
      )}{" "}
      {remove && (
        <ConfirmDialog
          title="Delete site"
          message={`Delete ${remove.name} and its dependent assets?`}
          pending={del.isPending}
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
  const [form, setForm] = useState(value);
  return (
    <Dialog
      title={value.id ? "Edit site" : "Add site"}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={pending} onClick={() => onSave(form)}>
            Save
          </Button>
        </>
      }
    >
      <div className="form-grid">
        <Field label="Name">
          <input
            value={form.name ?? ""}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        <Field label="Base URL">
          <input
            type="url"
            value={form.base_url ?? ""}
            onChange={(e) => setForm({ ...form, base_url: e.target.value })}
          />
        </Field>
        <Field label="Platform">
          <select
            value={form.platform}
            onChange={(e) => setForm({ ...form, platform: e.target.value })}
          >
            <option>openai-compatible</option>
            <option>new-api</option>
            <option>one-api</option>
          </select>
        </Field>
        <Field label="Status">
          <select
            value={form.status}
            onChange={(e) =>
              setForm({ ...form, status: e.target.value as Site["status"] })
            }
          >
            <option value="enabled">Enabled</option>
            <option value="disabled">Disabled</option>
          </select>
        </Field>
      </div>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

function Credentials() {
  const { client } = useSession();
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
  const run = useMutation({ mutationFn: service.runCredential });
  if (sites.isPending) return <Loading />;
  if (!sites.data?.length)
    return (
      <Panel>
        <Empty>Create a site before adding credentials.</Empty>
      </Panel>
    );
  return (
    <Panel
      actions={
        <>
          <select
            aria-label="Site"
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
            Add credential
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
          headers={["ID", "Kind", "Secret", "Status", "Scheduled", "Actions"]}
          empty={!creds.data?.length}
        >
          {creds.data?.map((c) => (
            <tr key={c.id}>
              <td>#{c.id}</td>
              <td>
                <strong>{c.kind}</strong>
              </td>
              <td>{c.has_secret ? "Stored" : "Missing"}</td>
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
                  label="Run check-in"
                  onClick={() => run.mutate(c.id)}
                >
                  <Play />
                </IconButton>
                <IconButton
                  label="Delete credential"
                  onClick={() => setRemove(c.id)}
                >
                  <Trash2 />
                </IconButton>
              </td>
            </tr>
          ))}
        </DataTable>
      )}
      {run.data && (
        <div className="result-strip">
          <StatusBadge value={run.data.status} />
          <span>
            {run.data.message}
            {run.data.reward ? ` · ${run.data.reward}` : ""}
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
      )}{" "}
      {remove && (
        <ConfirmDialog
          title="Delete credential"
          message={`Delete credential #${remove}? Channels using it may become unavailable.`}
          pending={del.isPending}
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
  const [kind, setKind] = useState("api_key"),
    [secret, setSecret] = useState(""),
    [meta, setMeta] = useState("");
  const submit = () => {
    onSave({ kind, secret, meta_json: meta, status: "enabled" });
    setSecret("");
  };
  return (
    <Dialog
      title="Add credential"
      onClose={() => {
        setSecret("");
        onClose();
      }}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={pending || !secret} onClick={submit}>
            Store credential
          </Button>
        </>
      }
    >
      <Field label="Kind">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="api_key">API key</option>
          <option value="session">Session</option>
          <option value="access_token">Access token</option>
          <option value="password">Password</option>
        </select>
      </Field>
      <Field label="Secret">
        <input
          type="password"
          autoComplete="new-password"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
      </Field>
      <Field
        label="Metadata JSON"
        hint='Optional, for example {"platform_user_id":123}'
      >
        <textarea value={meta} onChange={(e) => setMeta(e.target.value)} />
      </Field>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

function Channels() {
  const { client } = useSession();
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
  const [edit, setEdit] = useState<Partial<Channel> | null>(null),
    [remove, setRemove] = useState<Channel | null>(null);
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
      qc.invalidateQueries({ queryKey: ["channels"] });
      qc.invalidateQueries({ queryKey: ["routes"] });
    },
  });
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
          Add channel
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
            "Name",
            "Endpoint",
            "Models",
            "Priority / Weight",
            "Status",
            "Actions",
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
                  label="Refresh discovery"
                  onClick={() => refresh.mutate(c.id)}
                >
                  <RefreshCw className={refresh.isPending ? "spin" : ""} />
                </IconButton>
                <IconButton label="Edit channel" onClick={() => setEdit(c)}>
                  <Pencil />
                </IconButton>
                <IconButton label="Delete channel" onClick={() => setRemove(c)}>
                  <Trash2 />
                </IconButton>
              </td>
            </tr>
          ))}
        </DataTable>
      )}
      {refresh.data && (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>
            Found {refresh.data.models.length} models; created{" "}
            {refresh.data.created_routes} routes.
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
      )}{" "}
      {remove && (
        <ConfirmDialog
          title="Delete channel"
          message={`Delete ${remove.name} and its route memberships?`}
          pending={del.isPending}
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
  const [f, setF] = useState(value);
  return (
    <Dialog
      title={value.id ? "Edit channel" : "Add channel"}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={pending || !f.name} onClick={() => onSave(f)}>
            Save
          </Button>
        </>
      }
    >
      <div className="form-grid">
        <Field label="Name">
          <input
            value={f.name ?? ""}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label="Site">
          <select
            value={f.site_id ?? ""}
            onChange={(e) =>
              setF({ ...f, site_id: Number(e.target.value) || undefined })
            }
          >
            <option value="">None</option>
            {sites.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Credential ID">
          <input
            type="number"
            min="1"
            value={f.credential_id ?? ""}
            onChange={(e) =>
              setF({ ...f, credential_id: Number(e.target.value) || undefined })
            }
          />
        </Field>
        <Field label="Base URL">
          <input
            type="url"
            value={f.base_url ?? ""}
            onChange={(e) => setF({ ...f, base_url: e.target.value })}
          />
        </Field>
        <Field label="Type">
          <select
            value={f.type_hint}
            onChange={(e) => setF({ ...f, type_hint: e.target.value })}
          >
            <option>openai-compatible</option>
            <option>new-api</option>
          </select>
        </Field>
        <Field label="Models (comma separated)">
          <input
            value={f.models_csv ?? ""}
            onChange={(e) => setF({ ...f, models_csv: e.target.value })}
          />
        </Field>
        <Field label="Group">
          <input
            value={f.group_name ?? ""}
            onChange={(e) => setF({ ...f, group_name: e.target.value })}
          />
        </Field>
        <Field label="Priority">
          <input
            type="number"
            value={f.priority ?? 0}
            onChange={(e) => setF({ ...f, priority: Number(e.target.value) })}
          />
        </Field>
        <Field label="Weight">
          <input
            type="number"
            min="0"
            value={f.weight ?? 0}
            onChange={(e) => setF({ ...f, weight: Number(e.target.value) })}
          />
        </Field>
        <Field label="Status">
          <select
            value={f.status}
            onChange={(e) =>
              setF({ ...f, status: e.target.value as Channel["status"] })
            }
          >
            <option value="enabled">Enabled</option>
            <option value="disabled">Disabled</option>
          </select>
        </Field>
      </div>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}

function Keys() {
  const { client } = useSession();
  const service = api(client!);
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: ["keys"],
    queryFn: ({ signal }) => service.keys(signal),
  });
  const [add, setAdd] = useState(false),
    [created, setCreated] = useState<CreatedDownstreamKey | null>(null),
    [remove, setRemove] = useState<number | null>(null);
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
          Create key
        </Button>
      }
    >
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <ErrorState error={query.error} />
      ) : (
        <DataTable
          headers={["Name", "Scopes", "Status", "Created", "Actions"]}
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
                <IconButton label="Delete key" onClick={() => setRemove(k.id)}>
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
      )}{" "}
      {created && (
        <Dialog
          title="Copy your downstream key"
          onClose={() => setCreated(null)}
          actions={
            <Button onClick={() => setCreated(null)}>I have stored it</Button>
          }
        >
          <p className="warning">
            This token is shown once. It cannot be recovered after this dialog
            closes.
          </p>
          <div className="secret-output">
            <code>{created.token}</code>
            <IconButton
              label="Copy token"
              onClick={() => navigator.clipboard.writeText(created.token)}
            >
              <Copy />
            </IconButton>
          </div>
        </Dialog>
      )}
      {remove && (
        <ConfirmDialog
          title="Revoke downstream key"
          message="Clients using this key will immediately lose gateway access."
          confirmLabel="Revoke key"
          pending={del.isPending}
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
  const [name, setName] = useState(""),
    [scopes, setScopes] = useState("relay");
  return (
    <Dialog
      title="Create downstream key"
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={pending || !name.trim()}
            icon={<KeyRound size={16} />}
            onClick={() => onSave({ name, scopes })}
          >
            Create
          </Button>
        </>
      }
    >
      <Field label="Name">
        <input
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <Field label="Scopes">
        <input value={scopes} onChange={(e) => setScopes(e.target.value)} />
      </Field>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
