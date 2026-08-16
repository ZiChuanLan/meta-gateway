import { useQuery } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { api } from "../../api/client"
import { SearchableSelect, type SelectOption } from "../../components/SearchableSelect"
import { Button, Dialog, ErrorState, Field } from "../../components/ui"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"

export function CreateKeyDialog({
  channelName,
  channelId,
  pending,
  error,
  syncPending,
  onClose,
  onCreate,
  onSync,
}: {
  channelName: string;
  channelId: number;
  pending: boolean;
  error: unknown;
  syncPending: boolean;
  onClose: () => void;
  onCreate: (group: string) => void;
  onSync: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const groupsQuery = useQuery({
    queryKey: ["token-groups", channelId],
    queryFn: ({ signal }) => service.tokenGroups(channelId, signal),
    retry: false,
  });
  const [group, setGroup] = useState("");
  useEffect(() => {
    if (group) return;
    const groups = groupsQuery.data?.groups ?? [];
    if (groups.length > 0) setGroup(groups[0] ?? "");
  }, [groupsQuery.data, group]);
  const groupOptions: SelectOption[] = (groupsQuery.data?.groups ?? []).map(
    (value) => ({ value, label: value, group: "token-groups" }),
  );
  const groupsLoadFailed = Boolean(groupsQuery.isError);
  const canSubmit = !groupsLoadFailed && Boolean(group.trim());

  return (
    <Dialog
      title={t("channels.createKeyTitle")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            {t("common.cancel")}
          </Button>
          <Button
            // After a failure the token already exists upstream; further
            // clicks would create duplicate keys, so lock creation until the
            // dialog is dismissed (operator should sync instead).
            disabled={pending || !canSubmit || Boolean(error)}
            onClick={() => onCreate(group.trim())}
          >
            {t("channels.createKeyConfirm")}
          </Button>
        </>
      }
    >
      <p className="dialog-hint">
        {t("channels.createKeyHint")} <strong>{channelName}</strong>
      </p>
      <Field
        label={t("channels.createKeyGroup")}
        hint={t("channels.createKeyGroupHint")}
      >
        <SearchableSelect
          options={groupOptions}
          groups={["token-groups"]}
          value={group}
          onChange={(value) => setGroup(value ?? "")}
          disabled={pending}
          allowCustom
          placeholder={t("channels.createKeyGroupPlaceholder")}
        />
      </Field>
      {groupsLoadFailed ? (
        <div className="dialog-form-error">
          {t("channels.createKeyGroupsUnavailable")}
        </div>
      ) : null}
      {error ? (
        <div className="dialog-form-error">
          <ErrorState error={error} />
          <div className="create-key-sync-row">
            <Button
              variant="secondary"
              disabled={syncPending}
              onClick={onSync}
            >
              {t("channels.syncKeys")}
            </Button>
          </div>
        </div>
      ) : null}
    </Dialog>
  );
}
