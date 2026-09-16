package domain

// Catalog sync actions. A preview labels every field group so the console can
// explain exactly what a sync would do before it does it.
const (
	// CatalogActionCreate writes a row the registry has never seen.
	CatalogActionCreate = "create"
	// CatalogActionRefresh replaces a machine-generated row with catalog data.
	CatalogActionRefresh = "refresh"
	// CatalogActionFill fills metadata fields that are currently unset.
	CatalogActionFill = "fill"
	// CatalogActionSkipManual leaves an operator override alone.
	CatalogActionSkipManual = "skip_manual"
	// CatalogActionDisabled means the operator switched this field group off.
	CatalogActionDisabled = "disabled"
	// CatalogActionUnchanged means the catalog agrees with what is stored.
	CatalogActionUnchanged = "unchanged"
)

// CatalogFieldChange describes one field the sync would rewrite, in a form the
// console can render without knowing the underlying types.
type CatalogFieldChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// CatalogPreviewItem is the planned outcome for a single model.
type CatalogPreviewItem struct {
	Model  string   `json:"model"`
	Found  bool     `json:"found"`
	Source []string `json:"sources"`

	// Capability describes the protocol layer (endpoint, encoding, limits).
	CapabilityAction  string               `json:"capability_action"`
	CapabilitySource  string               `json:"capability_source"`
	CapabilityKind    string               `json:"capability_kind"`
	CapabilityChanges []CatalogFieldChange `json:"capability_changes,omitempty"`

	// Metadata describes context window / modalities / thinking / vendor.
	MetadataAction  string               `json:"metadata_action"`
	MetadataChanges []CatalogFieldChange `json:"metadata_changes,omitempty"`

	// Price describes the per-1k billing prices, kept separate because writing
	// them changes what a request costs.
	PriceAction  string               `json:"price_action"`
	PriceChanges []CatalogFieldChange `json:"price_changes,omitempty"`
}

// CatalogPreview is the read-only answer to "what would a sync change".
type CatalogPreview struct {
	Items         []CatalogPreviewItem `json:"items"`
	Requested     int                  `json:"requested"`
	Matched       int                  `json:"matched"`
	Missing       int                  `json:"missing"`
	Sources       []string             `json:"sources"`
	Errors        []string             `json:"errors"`
	PricesEnabled bool                 `json:"prices_enabled"`
	// Records whether the entries come from a live fetch or the caller's own
	// dataset (used by tests and by a future cache-as-source mode).
	Fetched bool `json:"fetched"`
}

// CatalogSyncState is the status board of the last applied sync.
type CatalogSyncState struct {
	SyncedAt      string   `json:"synced_at"`
	Requested     int      `json:"requested"`
	Matched       int      `json:"matched"`
	Capabilities  int      `json:"capabilities"`
	Metadata      int      `json:"metadata"`
	Prices        int      `json:"prices"`
	SkippedManual int      `json:"skipped_manual"`
	Missing       int      `json:"missing"`
	Sources       []string `json:"sources"`
	Errors        []string `json:"errors"`
}
