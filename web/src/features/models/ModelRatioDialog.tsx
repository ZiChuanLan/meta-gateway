import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Dialog, ErrorState, Field, Loading } from "../../components/ui";
import { useSession } from "../../session";
import { useI18n } from "../../i18n";
import { useUnsavedChanges } from "../../lib/unsavedChanges";

export function ModelRatioDialog({ model, onClose }: { model: string; onClose: () => void }) {
  const { client } = useSession();
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["model-ratios"],
    queryFn: ({ signal }) =>
      client!.get<Array<{ model: string; ratio: number }>>("/admin/ratios", signal),
  });
  return query.isPending ? (
    <Dialog title={t("billing.multiplierTitle", { model })} onClose={onClose}>
      <Loading />
    </Dialog>
  ) : query.isError ? (
    <Dialog title={t("billing.multiplierTitle", { model })} onClose={onClose}>
      <ErrorState error={query.error} retry={() => void query.refetch()} />
    </Dialog>
  ) : (
    <RatioEditor
      model={model}
      initial={query.data?.find((item) => item.model === model)?.ratio ?? 1}
      onClose={onClose}
    />
  );
}
function RatioEditor({
  model,
  initial,
  onClose,
}: {
  model: string;
  initial: number;
  onClose: () => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const qc = useQueryClient();
  const [value, setValue] = useState(String(initial));
  const dirty = value !== String(initial);
  const guard = useUnsavedChanges(dirty);
  const valid =
    value.trim() !== "" &&
    Number.isFinite(Number(value)) &&
    Number(value) >= 0 &&
    Number(value) <= 1000;
  const save = useMutation({
    mutationFn: () =>
      client!.request(`/admin/ratios/${encodeURIComponent(model)}`, {
        method: "PUT",
        body: JSON.stringify({ ratio: Number(value) }),
      }),
    onSuccess: async () => {
      await Promise.all(
        ["model-ratios", "model-pricing", "team"].map((key) =>
          qc.invalidateQueries({ queryKey: [key] }),
        ),
      );
      onClose();
    },
  });
  const close = () => {
    if (!save.isPending && guard()) onClose();
  };
  return (
    <Dialog title={t("billing.multiplierTitle", { model })} busy={save.isPending} onClose={close}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (valid && !save.isPending) save.mutate();
        }}
      >
        <Field label={t("billing.multiplier")} hint={t("billing.multiplierHint")}>
          <input
            type="number"
            min={0}
            max={1000}
            step="any"
            value={value}
            disabled={save.isPending}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        {Number(value) === 0 && value.trim() ? (
          <p role="status" className="panel-hint">
            {t("pricing.zeroRatio")}
          </p>
        ) : null}
        {save.isError ? <ErrorState error={save.error} /> : null}
        <div className="form-actions">
          <Button variant="secondary" onClick={close}>
            {t("common.close")}
          </Button>
          <Button type="submit" loading={save.isPending} disabled={!dirty || !valid}>
            {t("common.save")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
