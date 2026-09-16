package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// ModelCapabilityStore owns the model capability registry — the protocol layer
// (endpoint, encoding, input limits) that drives the image workbench and the
// chat-to-edit shim.
type ModelCapabilityStore struct {
	db *sql.DB
}

const capabilityColumns = `model, kind, provider, endpoints, input_formats, input_modalities,
	output_modalities, max_input_images, supports_stream, supports_tools, supports_json_mode,
	async_task, size_options, source, notes, updated_at`

func scanCapability(rows interface{ Scan(dest ...any) error }) (domain.Capability, error) {
	var (
		c         domain.Capability
		endpoints string
		inFormats string
		inModals  string
		outModals string
		stream    int
		tools     int
		jsonMode  int
		asyncTask int
	)
	err := rows.Scan(&c.Model, &c.Kind, &c.Provider, &endpoints, &inFormats, &inModals,
		&outModals, &c.MaxInputImages, &stream, &tools, &jsonMode, &asyncTask,
		&c.SizeOptions, &c.Source, &c.Notes, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.Endpoints = domain.SplitCSV(endpoints)
	c.InputFormats = domain.SplitCSV(inFormats)
	c.InputModalities = domain.SplitCSV(inModals)
	c.OutputModalities = domain.SplitCSV(outModals)
	c.SupportsStream = stream != 0
	c.SupportsTools = tools != 0
	c.SupportsJSONMode = jsonMode != 0
	c.AsyncTask = asyncTask != 0
	return c, nil
}

// Upsert inserts or replaces one capability row.
func (s *ModelCapabilityStore) Upsert(cap *domain.Capability) error {
	if cap == nil || strings.TrimSpace(cap.Model) == "" {
		return fmt.Errorf("model capability upsert: model is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT INTO model_capabilities (model, kind, provider, endpoints, input_formats, input_modalities,
		   output_modalities, max_input_images, supports_stream, supports_tools, supports_json_mode,
		   async_task, size_options, source, notes, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(model) DO UPDATE SET
		   kind = excluded.kind,
		   provider = excluded.provider,
		   endpoints = excluded.endpoints,
		   input_formats = excluded.input_formats,
		   input_modalities = excluded.input_modalities,
		   output_modalities = excluded.output_modalities,
		   max_input_images = excluded.max_input_images,
		   supports_stream = excluded.supports_stream,
		   supports_tools = excluded.supports_tools,
		   supports_json_mode = excluded.supports_json_mode,
		   async_task = excluded.async_task,
		   size_options = excluded.size_options,
		   source = excluded.source,
		   notes = excluded.notes,
		   updated_at = excluded.updated_at`,
		strings.TrimSpace(cap.Model),
		domain.NormalizeCapabilityKind(cap.Kind),
		strings.TrimSpace(cap.Provider),
		domain.JoinCSV(cap.Endpoints),
		domain.JoinCSV(cap.InputFormats),
		domain.JoinCSV(cap.InputModalities),
		domain.JoinCSV(cap.OutputModalities),
		cap.MaxInputImages,
		boolInt(cap.SupportsStream), boolInt(cap.SupportsTools),
		boolInt(cap.SupportsJSONMode), boolInt(cap.AsyncTask),
		strings.TrimSpace(cap.SizeOptions),
		domain.NormalizeCapabilitySource(cap.Source),
		strings.TrimSpace(cap.Notes),
		now,
	)
	if err != nil {
		return fmt.Errorf("model capability upsert: %w", err)
	}
	return nil
}

// List returns every persisted capability ordered by model name.
func (s *ModelCapabilityStore) List() ([]domain.Capability, error) {
	rows, err := s.db.Query(`SELECT ` + capabilityColumns + ` FROM model_capabilities ORDER BY model`)
	if err != nil {
		return nil, fmt.Errorf("model capability list: %w", err)
	}
	defer rows.Close()
	out := []domain.Capability{}
	for rows.Next() {
		c, err := scanCapability(rows)
		if err != nil {
			return nil, fmt.Errorf("model capability list scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get returns one row; nil when absent.
func (s *ModelCapabilityStore) Get(model string) (*domain.Capability, error) {
	row := s.db.QueryRow(`SELECT `+capabilityColumns+` FROM model_capabilities WHERE model = ?`,
		strings.TrimSpace(model))
	c, err := scanCapability(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("model capability get: %w", err)
	}
	return &c, nil
}

// Delete removes one row; missing rows are not an error.
func (s *ModelCapabilityStore) Delete(model string) error {
	if _, err := s.db.Exec(`DELETE FROM model_capabilities WHERE model = ?`, strings.TrimSpace(model)); err != nil {
		return fmt.Errorf("model capability delete: %w", err)
	}
	return nil
}

// Resolve returns the effective capability for a model: the persisted row when
// present, otherwise the built-in classifier result. The bool reports whether
// the answer came from the database.
func (s *ModelCapabilityStore) Resolve(model string) (domain.Capability, bool) {
	name := strings.TrimSpace(model)
	if stored, err := s.Get(name); err == nil && stored != nil {
		return *stored, true
	}
	inferred := domain.ClassifyModel(name)
	inferred.Model = name
	inferred.ResolvedByBuiltin = true
	return inferred, false
}

// ResolveMany resolves a batch of model names in one call.
func (s *ModelCapabilityStore) ResolveMany(models []string) map[string]domain.Capability {
	out := make(map[string]domain.Capability, len(models))
	for _, m := range models {
		name := strings.TrimSpace(m)
		if name == "" {
			continue
		}
		cap, _ := s.Resolve(name)
		out[name] = cap
	}
	return out
}

// AutoTag persists the built-in classification for a model the first time we
// see it. Rows already present are left untouched, and manual overrides are
// never replaced — otherwise a discovery sweep would silently discard an
// operator's correction.
// AutoTag persists the built-in name classification for a model.
//
// A row an operator edited, or one a curated catalog filled, is left alone: the
// first is a decision, the second carries limits, prices and modalities this
// classifier does not know. A row this same classifier produced is refreshed,
// so that improving a rule actually reaches the models already labelled — a
// row that stays wrong forever after the rule is fixed is worse than no row.
func (s *ModelCapabilityStore) AutoTag(model string) error {
	name := strings.TrimSpace(model)
	if name == "" {
		return nil
	}
	existing, err := s.Get(name)
	if err != nil {
		return err
	}
	if existing != nil && !domain.ClassifierOwned(existing.Source) {
		return nil
	}
	inferred := domain.ClassifyModel(name)
	inferred.Model = name
	inferred.Source = domain.CapabilitySourceDiscovery
	return s.Upsert(&inferred)
}
