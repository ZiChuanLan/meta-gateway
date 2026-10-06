package store

import (
	"errors"
	"log"

	"github.com/lan/meta-gateway/internal/domain"
)

// BillingLayer resolves the model-metadata layer. A configured public alias
// owns its price; otherwise its actual upstream name retains the existing
// model price. Selection is whole-layer, never a per-field merge.
func (s *ModelMetadataStore) BillingLayer(model, upstream string) (domain.PriceLayer, bool, error) {
	var failures error
	for index, name := range []string{model, upstream} {
		if name == "" || (index == 1 && name == model) {
			continue
		}
		meta, err := s.Get(name)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if meta == nil {
			continue
		}
		layer, err := domain.ResolvePriceLayer(meta.PricePromptPer1k, meta.PriceCompletionPer1k, meta.PriceCachePer1k, meta.PricePerRequest, meta.PriceTiers, meta.PriceSchedule)
		if err != nil {
			log.Printf("model pricing: discarded invalid rules for %q: %v", name, err)
		}
		if layer.Priced() {
			return layer, true, failures
		}
	}
	return domain.PriceLayer{}, false, failures
}
