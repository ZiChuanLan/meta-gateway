package httpapi

import (
	"net/http"

	"github.com/lan/meta-gateway/internal/store"
)

// Money display settings: the symbol and rate every amount is rendered
// through. Stored amounts never change — only their presentation does — so an
// operator can switch to their own currency without rewriting history.

// currencyView is what both the console and the member app receive.
type currencyView struct {
	Symbol string  `json:"symbol"`
	Rate   float64 `json:"rate"`
}

func currencyFor(settings store.DisplaySettings) currencyView {
	return currencyView{Symbol: settings.CurrencySymbol, Rate: settings.CurrencyRate}
}

// displaySettings is the console's read of the display preferences.
func (h *AdminHandler) displaySettings(w http.ResponseWriter, r *http.Request) {
	if h.db == nil || h.db.Display == nil {
		writeJSON(w, http.StatusOK, currencyView{Symbol: store.DefaultCurrencySymbol, Rate: 1})
		return
	}
	writeJSON(w, http.StatusOK, currencyFor(h.db.Display.Get()))
}

// saveDisplaySettings validates and stores them. The symbol is a display
// string (it may be "¥", "CNY " or "€"), so the check is length and
// non-emptiness; the rate is bounded to keep a typo from turning every amount
// into nonsense.
func (h *AdminHandler) saveDisplaySettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol string  `json:"symbol"`
		Rate   float64 `json:"rate"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	settings := store.DisplaySettings{CurrencySymbol: req.Symbol, CurrencyRate: req.Rate}
	if err := h.db.Display.Save(settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, currencyFor(h.db.Display.Get()))
}
