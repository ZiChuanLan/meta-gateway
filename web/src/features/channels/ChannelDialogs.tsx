import type { ComponentProps } from "react";
import type { Channel, ChannelOverview, Credential, Site } from "../../api/types";
import { Button, ConfirmDialog } from "../../components/ui";
import { Drawer } from "../../components/Drawer";
import { AddChannelDialog } from "./AddChannelDialog";
import { CreateKeyDialog } from "./CreateKeyDialog";
import { EditChannelDialog } from "./EditChannelDialog";
import { ChannelModelsPanel } from "../ChannelModels";
import { ChannelKeysDrawer } from "../ChannelKeys";
import type { CreateConnectionInput } from "./helpers";
import type { PickableCredential } from "./channelCredentials";
import { useToast } from "../../toast";

/** The toast context these dialogs push through. */
type ToastContextValue = ReturnType<typeof useToast>;

/**
 * Every overlay the channels board can open, in one place.
 *
 * These were the last ~190 lines of the page component: six independent dialogs
 * and drawers, each wired to a different slice of state, all interleaved with the
 * board's own layout. Nothing about them depends on the board's position in the
 * tree — only on what to open and what to do when the operator confirms — so they
 * are parameters here instead of captured closures.
 *
 * Pure wiring on purpose: no query, no state of its own, no invalidation. The
 * board remains the owner of every piece of data these dialogs touch.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** The slice of a mutation hook these dialogs touch. */
type Mutation = {
  isPending: boolean;
  error: unknown;
  pendingId?: number | string | null;
  reset: () => void;
  /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
  mutate: (...args: any[]) => unknown;
};

// The dialog owns this type; deriving it here keeps the two in step.
type RouteOverview = ComponentProps<typeof EditChannelDialog>["routeOverviews"];

export type ChannelDialogsProps = {
  t: Text;
  toast: ToastContextValue;
  params: URLSearchParams;
  setParams: (next: URLSearchParams, options?: { replace: boolean }) => void;
  siteById: Map<number, Site>;
  overviews: ChannelOverview[];
  routeOverviews: RouteOverview;
  credentials: Credential[];
  userCredentialFor: (overview: ChannelOverview) => PickableCredential | undefined;
  relayCredentialFor: (overview: ChannelOverview) => PickableCredential | undefined;
  addOpen: boolean;
  setAddOpen: (open: boolean) => void;
  edit: Channel | null;
  setEdit: (channel: Channel | null) => void;
  modelsChannel: Channel | null;
  setModelsChannel: (channel: Channel | null) => void;
  keysChannel: Channel | null;
  setKeysChannel: (channel: Channel | null) => void;
  createKeyChannel: Channel | null;
  setCreateKeyChannel: (channel: Channel | null) => void;
  remove: Channel | null;
  setRemove: (channel: Channel | null) => void;
  /** Synchronous re-entry guard for the create-key dialog. */
  createKeyLocked: { current: boolean };
  closeModelsDrawer: () => void;
  submitCreate: (value: CreateConnectionInput, options: { verify: boolean }) => void;
  mutations: {
    createConnection: Mutation;
    saveEdit: Mutation;
    setCredentialStatus: Mutation;
    addApiKeyCredential: Mutation;
    deleteApiKeyCredential: Mutation;
    createUpstreamKey: Mutation;
    syncKeys: Mutation;
    updateKeyModels: Mutation;
    updateKeyPriority: Mutation;
    refresh: Mutation;
    del: Mutation;
  };
};

/** The board's copy of "which credential belongs to this channel". */
function credentialForEdit(
  edit: Channel,
  overviews: ChannelOverview[],
  pick: (overview: ChannelOverview) => PickableCredential | undefined,
) {
  const overview = overviews.find((row) => row.channel.id === edit.id) ?? null;
  return overview ? pick(overview) : undefined;
}

export function ChannelDialogs(props: ChannelDialogsProps) {
  const {
    t,
    toast,
    params,
    setParams,
    siteById,
    overviews,
    routeOverviews,
    credentials,
    userCredentialFor,
    relayCredentialFor,
    addOpen,
    setAddOpen,
    edit,
    setEdit,
    modelsChannel,
    setModelsChannel,
    keysChannel,
    setKeysChannel,
    createKeyChannel,
    setCreateKeyChannel,
    remove,
    setRemove,
    createKeyLocked,
    closeModelsDrawer,
    submitCreate,
    mutations,
  } = props;
  return (
    <>
      {addOpen ? (
        <AddChannelDialog
          pending={mutations.createConnection.isPending}
          error={mutations.createConnection.error}
          onClose={() => {
            if (mutations.createConnection.isPending) return;
            setAddOpen(false);
          }}
          onSave={(value, options) => submitCreate(value, options)}
        />
      ) : null}
      {edit ? (
        <EditChannelDialog
          // Remount per channel: every field seeds from `value`, so reusing the
          // instance across rows would leave the previous channel's values
          // (notably the sync mode radio) in the form.
          key={edit.id}
          value={edit}
          routeOverviews={routeOverviews}
          site={edit.site_id != null ? siteById.get(edit.site_id) : undefined}
          credentials={credentials}
          credential={credentialForEdit(edit, overviews, relayCredentialFor)}
          userCredential={credentialForEdit(edit, overviews, userCredentialFor)}
          checkinSupported={
            (overviews.find((row) => row.channel.id === edit.id) ?? null)?.checkin_supported ??
            false
          }
          pending={
            mutations.saveEdit.isPending ||
            mutations.setCredentialStatus.isPending ||
            mutations.addApiKeyCredential.isPending ||
            mutations.deleteApiKeyCredential.isPending
          }
          error={
            mutations.saveEdit.error ??
            mutations.setCredentialStatus.error ??
            mutations.addApiKeyCredential.error ??
            mutations.deleteApiKeyCredential.error
          }
          onClose={() => {
            setEdit(null);
            setModelsChannel(null);
            setKeysChannel(null);
          }}
          onSave={(value) => mutations.saveEdit.mutate(value)}
          onManageModels={() => {
            setKeysChannel(null);
            setModelsChannel(edit);
          }}
          onManageKeys={() => {
            setModelsChannel(null);
            setKeysChannel(edit);
          }}
          onRefreshModels={() => {
            mutations.refresh.reset();
            mutations.refresh.mutate(edit.id);
          }}
          refreshingModels={mutations.refresh.pendingId === edit.id}
        />
      ) : null}
      <ChannelAuxDialogs
        t={t}
        toast={toast}
        params={params}
        setParams={setParams}
        credentials={credentials}
        createKeyChannel={createKeyChannel}
        setCreateKeyChannel={setCreateKeyChannel}
        modelsChannel={modelsChannel}
        closeModelsDrawer={closeModelsDrawer}
        keysChannel={keysChannel}
        setKeysChannel={setKeysChannel}
        remove={remove}
        setRemove={setRemove}
        createKeyLocked={createKeyLocked}
        mutations={mutations}
      />
    </>
  );
}

/** The rest of the overlays: key creation, the two channel drawers, and delete. */
function ChannelAuxDialogs({
  t,
  toast,
  params,
  setParams,
  credentials,
  createKeyChannel,
  setCreateKeyChannel,
  modelsChannel,
  closeModelsDrawer,
  keysChannel,
  setKeysChannel,
  remove,
  setRemove,
  createKeyLocked,
  mutations,
}: Pick<
  ChannelDialogsProps,
  | "t"
  | "toast"
  | "params"
  | "setParams"
  | "credentials"
  | "createKeyChannel"
  | "setCreateKeyChannel"
  | "modelsChannel"
  | "closeModelsDrawer"
  | "keysChannel"
  | "setKeysChannel"
  | "remove"
  | "setRemove"
  | "createKeyLocked"
  | "mutations"
>) {
  return (
    <>
      {createKeyChannel ? (
        <CreateKeyDialog
          channelName={createKeyChannel.name}
          channelId={createKeyChannel.id}
          pending={mutations.createUpstreamKey.isPending}
          error={mutations.createUpstreamKey.error}
          onClose={() => {
            if (mutations.createUpstreamKey.isPending) return;
            mutations.createUpstreamKey.reset();
            setCreateKeyChannel(null);
          }}
          onCreate={(group) => {
            // Synchronous re-entry guard: the disabled={pending} button only
            // takes effect after re-render, so rapid double-clicks could
            // otherwise create several upstream tokens.
            if (createKeyLocked.current) return;
            createKeyLocked.current = true;
            const input = {
              id: createKeyChannel.id,
              name: `gateway-${group || "default"}`,
              group,
            };
            mutations.createUpstreamKey.mutate(input, {
              onSuccess: () => {
                // Close immediately so the operator cannot click again; the
                // toast is the success signal.
                mutations.createUpstreamKey.reset();
                setCreateKeyChannel(null);
                toast.push({
                  tone: "success",
                  message: t("channels.createKeySuccess", { name: createKeyChannel.name }),
                });
              },
              onSettled: () => {
                createKeyLocked.current = false;
              },
            });
          }}
          // If the upstream masks the fresh key, offer a one-click sync import
          // inside the dialog instead of forcing a manual paste.
          syncPending={mutations.syncKeys.isPending}
          onSync={() => {
            mutations.createUpstreamKey.reset();
            mutations.syncKeys.reset();
            mutations.syncKeys.mutate(createKeyChannel.id);
          }}
        />
      ) : null}
      {modelsChannel ? (
        <Drawer
          title={t("channels.modelsSection")}
          width={780}
          onClose={closeModelsDrawer}
          footer={
            <Button variant="secondary" onClick={closeModelsDrawer}>
              {t("common.close")}
            </Button>
          }
        >
          <ChannelModelsPanel
            channelId={modelsChannel.id}
            header={
              <div className="channel-models-panel-head">
                <div>
                  <p className="page-kicker">{modelsChannel.name}</p>
                  <p className="detail-section-empty is-quiet">{t("channels.modelsManageHint")}</p>
                </div>
              </div>
            }
          />
        </Drawer>
      ) : null}
      {keysChannel ? (
        <ChannelKeysDrawer
          channel={keysChannel}
          apiKeys={credentials.filter((item) => item.kind === "api_key")}
          pending={
            mutations.setCredentialStatus.isPending || mutations.deleteApiKeyCredential.isPending
          }
          addApiKeyPending={mutations.addApiKeyCredential.isPending}
          syncKeysPending={mutations.syncKeys.isPending}
          onToggleKey={(id, enabled) =>
            mutations.setCredentialStatus.mutate({
              id,
              status: enabled ? "enabled" : "disabled",
            })
          }
          onUpdateKeyModels={(id, modelsCsv) => mutations.updateKeyModels.mutate({ id, modelsCsv })}
          onUpdateKeyPriority={(id, priority) =>
            mutations.updateKeyPriority.mutate({ id, priority })
          }
          onDeleteKey={(id) => mutations.deleteApiKeyCredential.mutate(id)}
          onAddApiKey={(secret, name) => {
            const siteId = keysChannel.site_id;
            if (!siteId) return;
            mutations.addApiKeyCredential.mutate({ siteId, secret, name });
          }}
          onSyncKeys={() => {
            mutations.syncKeys.reset();
            mutations.syncKeys.mutate(keysChannel.id);
          }}
          onClose={() => {
            setKeysChannel(null);
            // Strip the deep-link param, or the effect re-opens on the next
            // render with the stale snapshot (same trap as ?channel=).
            if (params.has("keys")) {
              const next = new URLSearchParams(params);
              next.delete("keys");
              setParams(next, { replace: true });
            }
          }}
        />
      ) : null}
      {remove ? (
        <ConfirmDialog
          title={t("channels.deleteTitle")}
          message={t("channels.deleteMsg", { name: remove.name })}
          pending={mutations.del.isPending}
          error={mutations.del.error}
          onClose={() => setRemove(null)}
          onConfirm={() => mutations.del.mutate(remove.id)}
        />
      ) : null}
    </>
  );
}
