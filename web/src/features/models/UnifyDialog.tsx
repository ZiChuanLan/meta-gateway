import { AlertTriangle, Check, Merge } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../../api/client";
import type {
  UnifyApplyResult,
  UnifyGroup,
  UnifyRule,
  UnifyVariant,
} from "../../api/types";
import { Button, Dialog, Empty } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

const UNIFY_INVALIDATE_KEYS = [
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["channel-overviews"],
  ["models"],
  ["explain"],
  ["missing-models"],
  ["unify-preview"],
] as const;

// Every rule the pipeline knows, in application order. All on by default so a
// name reaches its simplest form in one pass; turning one off is how an
// operator keeps a suffix that actually carries meaning.
const ALL_RULES: UnifyRule[] = [
  "account_prefix",
  "vendor_prefix",
  "date_suffix",
  "index_suffix",
];

/**
 * One-click model-name unification: scans discovered channel models and folds
 * every variant of one logical model onto a single canonical name.
 *
 * Rules compose rather than compete — with the vendor and date rules both on,
 * deepseek-ai/deepseek-v4-flash-0731 reaches deepseek-v4-flash directly
 * instead of having to be merged twice. A group that needed a rule capable of
 * conflating different models is flagged risky and starts unchecked.
 */
export function UnifyDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [checked, setChecked] = useState<Record<string, boolean>>({});
  const [result, setResult] = useState<UnifyApplyResult | null>(null);
  const [rules, setRules] = useState<UnifyRule[]>(ALL_RULES);

  const preview = useQuery({
    queryKey: ["unify-preview", rules],
    queryFn: () => service.unifyPreview(rules),
    retry: false,
  });

  const groups = preview.data?.groups ?? [];
  const archived = preview.data?.archived ?? [];
  // One list, split by whether the merge needed a rule that can conflate
  // genuinely different models.
  const safeGroups = groups.filter((group) => !group.risky);
  const riskyGroups = groups.filter((group) => group.risky);

  const isChecked = (key: string, fallback: boolean) =>
    checked[key] ?? fallback;
  const toggle = (key: string, value: boolean) =>
    setChecked((current) => ({ ...current, [key]: value }));

  const selectedGroups: UnifyGroup[] = [
    ...safeGroups.filter((group) => isChecked(`s:${group.canonical}`, true)),
    ...riskyGroups.filter((group) => isChecked(`r:${group.canonical}`, false)),
  ];

  const apply = useAdminMutation({
    // Hiding the superseded originals is what an alias means — the server
    // archives every route the group provably covers and skips the rest.
    mutationFn: (input: UnifyGroup[]) => service.unifyApply(input),
    invalidateKeys: [...UNIFY_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: (data) => setResult(data),
  });

  const empty = !preview.isPending && groups.length === 0;

  return (
    <Dialog title={t("modelsPage.unify.title")} onClose={onClose}>
      <p className="unify-intro">{t("modelsPage.unify.description")}</p>

      {result ? (
        <div className="unify-result" role="status">
          <Check size={14} />
          {t("modelsPage.unify.result", {
            routes: result.routes_created,
            members: result.members_created,
            skipped: result.members_skipped,
            archived: result.routes_archived,
          })}
        </div>
      ) : null}

      <section className="unify-section">
        <h3>{t("modelsPage.unify.rulesSection")}</h3>
        <p className="unify-section-hint">
          {t("modelsPage.unify.rulesHint")}
        </p>
        {ALL_RULES.map((rule) => (
          <label className="check" key={rule}>
            <input
              type="checkbox"
              checked={rules.includes(rule)}
              disabled={preview.isPending}
              onChange={(event) => {
                const next = event.target.checked
                  ? ALL_RULES.filter(
                      (item) => item === rule || rules.includes(item),
                    )
                  : rules.filter((item) => item !== rule);
                setRules(next);
                setChecked({});
              }}
            />
            <span>{t(`modelsPage.unify.rule.${rule}`)}</span>
          </label>
        ))}
      </section>

      {preview.isPending ? (
        <Empty>{t("common.loading")}</Empty>
      ) : preview.isError ? (
        <div className="inline-error">{String(preview.error)}</div>
      ) : empty ? (
        <Empty>{t("modelsPage.unify.empty")}</Empty>
      ) : (
        <div className="unify-body">
          {safeGroups.length > 0 ? (
            <section className="unify-section">
              <h3>{t("modelsPage.unify.safeSection")}</h3>
              {safeGroups.map((group) => (
                <GroupCard
                  key={group.canonical}
                  group={group}
                  checked={isChecked(`s:${group.canonical}`, true)}
                  onToggle={(value) => toggle(`s:${group.canonical}`, value)}
                />
              ))}
            </section>
          ) : null}
          {riskyGroups.length > 0 ? (
            <section className="unify-section is-loose">
              <h3>
                <AlertTriangle size={14} />
                {t("modelsPage.unify.riskySection")}
              </h3>
              <p className="unify-section-hint">
                {t("modelsPage.unify.riskyHint")}
              </p>
              {riskyGroups.map((group) => (
                <GroupCard
                  key={group.canonical}
                  group={group}
                  checked={isChecked(`r:${group.canonical}`, false)}
                  onToggle={(value) => toggle(`r:${group.canonical}`, value)}
                />
              ))}
            </section>
          ) : null}
        </div>
      )}

      {empty && archived.length > 0 ? (
        <p className="unify-section-hint">
          {t("modelsPage.unify.archivedNote", { count: archived.length })}
        </p>
      ) : null}
      {apply.error ? (
        <div className="inline-error">{String(apply.error)}</div>
      ) : null}
      <div className="dialog-actions">
        <Button variant="secondary" disabled={apply.isPending} onClick={onClose}>
          {t("common.close")}
        </Button>
        <Button
          icon={<Merge size={15} />}
          disabled={apply.isPending || selectedGroups.length === 0}
          onClick={() => apply.mutate(selectedGroups)}
        >
          {apply.isPending
            ? t("common.working")
            : t("modelsPage.unify.apply", { count: selectedGroups.length })}
        </Button>
      </div>
    </Dialog>
  );
}

function GroupCard({
  group,
  checked,
  onToggle,
}: {
  group: UnifyGroup;
  checked: boolean;
  onToggle: (value: boolean) => void;
}) {
  const { t } = useI18n();
  const [showCovered, setShowCovered] = useState(false);
  const covered = group.mapped_count ?? 0;
  const total = group.variants.length;
  const pendingNew = total - covered;
  const pendingVariants = group.variants.filter((variant) => !variant.mapped);
  const coveredVariants = group.variants.filter((variant) => variant.mapped);
  // Rows for variants that still need merging stay visible; already-covered
  // rows collapse behind a one-line toggle so a 23-variant group does not
  // drown the preview in "covered" noise (54 of every 135 rows today).
  const renderVariant = (variant: UnifyVariant) => (
    <li key={`${variant.channel_id}:${variant.model_name}`}>
      <span className="unify-channel">{variant.channel_name}</span>
      <span className="unify-count">
        {t("modelsPage.unify.originalPrefix")}
      </span>
      <span className="mono">{variant.model_name}</span>
      {/* Only show the rewrite when the original actually differs;
          printing "x → x" for every row was unreadable. */}
      {variant.model_name === group.canonical ? (
        <span className="unify-count">
          {t("modelsPage.unify.nativeName")}
        </span>
      ) : (
        <>
          <span className="unify-arrow">→</span>
          <span className="mono">{group.canonical}</span>
        </>
      )}
      {variant.mapped ? (
        <span className="model-meta-badge is-mapped">
          {t("modelsPage.unify.mapped")}
        </span>
      ) : (
        <span className="model-meta-badge">
          {t("modelsPage.unify.pending")}
        </span>
      )}
    </li>
  );
  return (
    <div
      className={`unify-group${group.risky ? " is-loose" : ""}${checked ? "" : " is-off"}`}
    >
      <label className="unify-group-head">
        <input
          type="checkbox"
          checked={checked}
          onChange={(event) => onToggle(event.target.checked)}
        />
        <span className="unify-count">
          {t("modelsPage.unify.canonicalPrefix")}
        </span>
        <strong className="mono">{group.canonical}</strong>
        {group.route_id ? (
          <span className="model-meta-badge">
            {t("modelsPage.unify.routeExists")}
          </span>
        ) : null}
        <span className="unify-count">
          {t("modelsPage.unify.progress", { done: covered, total })}
        </span>
        {group.rules?.map((rule) => (
          <span className="model-meta-badge" key={rule}>
            {t(`modelsPage.unify.rule.${rule}`)}
          </span>
        ))}
        {pendingNew > 0 ? (
          <span className="model-meta-badge is-mapped">
            {t("modelsPage.unify.pendingCount", { count: pendingNew })}
          </span>
        ) : (
          <span className="model-meta-badge">
            {t("modelsPage.unify.allMapped")}
          </span>
        )}
      </label>
      <ul className="unify-variants">
        {pendingVariants.map(renderVariant)}
        {coveredVariants.length > 0 ? (
          <li>
            <button
              type="button"
              className="unify-covered-toggle"
              onClick={() => setShowCovered((value) => !value)}
            >
              {showCovered
                ? t("modelsPage.unify.collapseCovered")
                : t("modelsPage.unify.showCovered", {
                    count: coveredVariants.length,
                  })}
            </button>
          </li>
        ) : null}
        {showCovered ? coveredVariants.map(renderVariant) : null}
      </ul>
    </div>
  );
}
