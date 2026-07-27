package account

import "context"

// ExchangeKeySyncer adapts account.Service to exchange.KeySyncer.
type ExchangeKeySyncer struct {
	Service *Service
}

func (a ExchangeKeySyncer) SyncKeys(ctx context.Context, channelID int64) (created, reused, masked int, category string, err error) {
	if a.Service == nil {
		return 0, 0, 0, "unavailable", nil
	}
	split := false
	result, syncErr := a.Service.SyncKeys(ctx, channelID, SyncKeysRequest{SplitByGroup: &split})
	if syncErr != nil {
		return 0, 0, 0, "", syncErr
	}
	if result == nil {
		return 0, 0, 0, "empty", nil
	}
	return result.CreatedCredentials, result.ReusedCredentials, result.SkippedMasked, result.Category, nil
}
