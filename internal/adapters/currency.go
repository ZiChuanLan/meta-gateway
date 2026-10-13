package adapters

import "strings"

// CurrencySymbolFor resolves what a site prints for money: the ISO code first,
// the site's own declaration only when the code is unknown. Both the account
// price path and the public status page read /api/status, and both must read it
// the same way.
//
// A known ISO code wins over the deployment's custom symbol: measured, sites
// declare CNY while carrying custom_currency_symbol "¤" (the generic placeholder
// meaning "unspecified"), "Ɇ" (a mis-encoded ¥) or even a fish-cake emoji, and
// "¤30" tells an operator nothing. The custom symbol is only used when the
// currency itself is unknown — that is the case it was meant for.
func CurrencySymbolFor(currency, declared string) string {
	if symbol := DefaultCurrencySymbol(currency); symbol != "" {
		return symbol
	}
	trimmed := strings.TrimSpace(declared)
	if trimmed == "¤" {
		return ""
	}
	return trimmed
}

// DefaultCurrencySymbol fills in the sign for the currencies the fleet actually
// declares. It only applies when a site leaves custom_currency_symbol empty.
func DefaultCurrencySymbol(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return "$"
	case "CNY", "RMB", "JPY":
		return "¥"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "HKD":
		return "HK$"
	case "KRW":
		return "₩"
	default:
		return ""
	}
}
