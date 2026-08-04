package checkin

import (
	"context"
	"errors"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestRunAllContinuesAfterInternalError(t *testing.T) {
	// Regression: an internal failure without a persisted result (e.g. transient
	// DB error) must become a synthetic failed item and not abort the batch.
	creds := []domain.Credential{
		{ID: 1, SiteID: 10},
		{ID: 2, SiteID: 20},
		{ID: 3, SiteID: 30},
	}
	var attempted []int64
	runOne := func(_ context.Context, id int64, _ string, _ bool) (RunResult, error) {
		attempted = append(attempted, id)
		switch id {
		case 1:
			return RunResult{}, &Error{Kind: ErrorInternal, Category: "site_lookup"}
		case 2:
			return RunResult{SiteID: 20, CredentialID: 2, Status: StatusSuccess}, nil
		default:
			return RunResult{SiteID: 30, CredentialID: 3, Status: StatusSkipped}, nil
		}
	}
	summary, err := runAll(t.Context(), creds, SourceScheduled, runOne)
	if err != nil {
		t.Fatalf("batch must not abort on internal error: %v", err)
	}
	if len(summary.Items) != 3 || summary.SuccessCount != 1 || summary.FailureCount != 1 || summary.SkippedCount != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	// All credentials were attempted despite the first failing internally.
	if len(attempted) != 3 {
		t.Fatalf("attempted=%v, want all three", attempted)
	}
	item := summary.Items[0]
	if item.CredentialID != 1 || item.SiteID != 10 || item.Status != StatusFailed || item.Category != "internal" {
		t.Fatalf("synthetic item=%+v", item)
	}
	if summary.Items[1].CredentialID != 2 || summary.Items[2].CredentialID != 3 {
		t.Fatalf("items=%+v", summary.Items)
	}
}

func TestRunAllAbortsOnContextCancellation(t *testing.T) {
	creds := []domain.Credential{{ID: 1, SiteID: 10}, {ID: 2, SiteID: 20}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runAll(ctx, creds, SourceScheduled, func(context.Context, int64, string, bool) (RunResult, error) {
		t.Fatal("runOne must not be called after cancellation")
		return RunResult{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}
