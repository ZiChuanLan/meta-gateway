import { useI18n } from "../../i18n";

/** Compact family chips, shared by both catalogue roles. */
export function ModelFamilyFilters({
  groups,
  value,
  onChange,
}: {
  groups: string[];
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="model-family-filters" role="group" aria-label={t("modelsPage.groupFilter")}>
      {["", ...groups].map((group) => (
        <button
          type="button"
          key={group}
          aria-pressed={value === group}
          onClick={() => onChange(group)}
        >
          {group || t("modelsPage.allGroups")}
        </button>
      ))}
    </div>
  );
}
