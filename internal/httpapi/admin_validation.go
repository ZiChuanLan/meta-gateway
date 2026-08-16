package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
)

// pathID parses a positive integer path parameter (e.g. /channels/{id}) and
// writes a 400 for unparsable or non-positive values, so no handler funnels
// id=0 or negative ids into the store. Returns ok=false after writing.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

// validCredentialStatus accepts every status the rest of the system can write
// (auto_disabled rows exist even though only channels usually get it), so a
// client echoing a stored value back never 400s.
func validCredentialStatus(status string) bool {
	return status == domain.StatusEnabled || status == domain.StatusDisabled || status == domain.StatusAutoDisabled
}

func validRoutingMode(mode string) bool {
	return mode == "" ||
		mode == domain.RoutingModeAuto ||
		mode == domain.RoutingModeLatency ||
		mode == domain.RoutingModeWeighted ||
		mode == domain.RoutingModeAdaptive ||
		mode == domain.RoutingModeSingle
}

// validateSinglePin keeps routing_mode=single consistent: the pin must belong
// to the route being saved. A single route without a pin evaluates as auto
// (fall-back), but a pin pointing at another route's member is a client bug.
func (h *AdminHandler) validateSinglePin(route *domain.Route) error {
	if route.RoutingMode != domain.RoutingModeSingle || route.SingleMemberID == nil {
		return nil
	}
	member, err := h.db.RouteMember.GetByID(*route.SingleMemberID)
	if err != nil {
		return err
	}
	if member == nil || member.RouteID != route.ID {
		return errors.New("single_member_id must be a member of this route")
	}
	return nil
}

// validateRouteRetryOverrides keeps the model-level policy within the same
// bounds as the global runtime policy. The UI supplies these limits too, but
// the API must enforce them because routes can be edited by any Admin client.
func validateRouteRetryOverrides(route *domain.Route) error {
	if route == nil {
		return errors.New("route is required")
	}
	if route.RetryTimes != nil && (*route.RetryTimes < 0 || *route.RetryTimes > 100) {
		return errors.New("retry_times must be between 0 and 100")
	}
	if route.ChannelRetryTimes != nil && (*route.ChannelRetryTimes < 0 || *route.ChannelRetryTimes > 5) {
		return errors.New("channel_retry_times must be between 0 and 5")
	}
	return nil
}

func validateCustomDownstreamToken(token string) error {
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must not contain whitespace")
	}
	// Allow operator-chosen secrets (OpenAI-style sk-…, random strings, etc.).
	if len(token) < 16 {
		return errors.New("token must be at least 16 characters")
	}
	if len(token) > 256 {
		return errors.New("token must be at most 256 characters")
	}
	for _, r := range token {
		if r < 32 || r == 127 {
			return errors.New("token contains invalid control characters")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Proxy Logs
// ---------------------------------------------------------------------------
