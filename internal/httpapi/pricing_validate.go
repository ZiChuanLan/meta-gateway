package httpapi

import (
	"fmt"

	"github.com/lan/meta-gateway/internal/domain"
)

// Price validation lives at the HTTP boundary because that is where an
// operator's input crosses: a malformed ladder stored now would be parsed on
// every relay afterwards. Each helper both checks the value and re-encodes it,
// so what the billing path reads is always what was approved here.
//
// Both are tolerant of an empty value ("no ladder", "no schedule") — that is
// the default and it bills exactly as it did before these existed.

// validateMemberPricing checks and normalizes a route member's ladder and
// schedule in place.
func validateMemberPricing(member *domain.RouteMember) error {
	tiers, err := checkedPriceTiers(member.PriceTiers)
	if err != nil {
		return err
	}
	windows, err := checkedPriceWindows(member.PriceSchedule)
	if err != nil {
		return err
	}
	member.PriceTiers, member.PriceSchedule = tiers, windows
	return nil
}

// validateModelPricing checks and normalizes a model's ladder and schedule.
func validateModelPricing(meta *domain.ModelMetadata) error {
	tiers, err := checkedPriceTiers(meta.PriceTiers)
	if err != nil {
		return err
	}
	windows, err := checkedPriceWindows(meta.PriceSchedule)
	if err != nil {
		return err
	}
	meta.PriceTiers, meta.PriceSchedule = tiers, windows
	return nil
}

func checkedPriceTiers(raw string) (string, error) {
	tiers, err := domain.ParsePriceTiers(raw)
	if err != nil {
		return "", fmt.Errorf("price_tiers: %w", err)
	}
	if err := domain.ValidatePriceTiers(tiers); err != nil {
		return "", fmt.Errorf("price_tiers: %w", err)
	}
	encoded, err := domain.EncodePriceTiers(tiers)
	if err != nil {
		return "", err
	}
	return encoded, nil
}

func checkedPriceWindows(raw string) (string, error) {
	windows, err := domain.ParsePriceWindows(raw)
	if err != nil {
		return "", fmt.Errorf("price_schedule: %w", err)
	}
	if err := domain.ValidatePriceWindows(windows); err != nil {
		return "", fmt.Errorf("price_schedule: %w", err)
	}
	encoded, err := domain.EncodePriceWindows(windows)
	if err != nil {
		return "", err
	}
	return encoded, nil
}
