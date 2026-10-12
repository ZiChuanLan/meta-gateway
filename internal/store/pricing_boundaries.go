package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

var ErrPriceConflict = errors.New("pricing changed; reload the rule before saving")

func readBillingRule(ex sqlExecutor, scope, key string) (*domain.BillingRule, error) {
	var raw string
	var rev int64
	var at string
	err := ex.QueryRow("SELECT payload,revision,updated_at FROM billing_rules WHERE scope=? AND scope_key=?", scope, key).Scan(&raw, &rev, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r domain.BillingRule
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	r.Revision = rev
	r.UpdatedAt = at
	return &r, nil
}
func (db *DB) BillingRule(scope, key string) (*domain.BillingRule, error) {
	return readBillingRule(db.DB, scope, key)
}

// SaveBillingRule compares the version in the same write transaction. A missing
// rule has revision 0. No legacy column is cleared or rewritten.
func (db *DB) SaveBillingRule(scope, key string, r domain.BillingRule, expected int64) (*domain.BillingRule, error) {
	if scope != "model" && scope != "member" {
		return nil, fmt.Errorf("invalid rule scope")
	}
	if key == "" || len(key) > 512 {
		return nil, fmt.Errorf("invalid rule key")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Acquire SQLite's writer reservation before read/check/write. Concurrent
	// edits serialize here rather than upgrading a stale deferred read snapshot.
	if _, err = tx.Exec("UPDATE billing_rules SET revision=revision WHERE scope=? AND scope_key=?", scope, key); err != nil {
		return nil, err
	}
	if r.QuoteID > 0 {
		quote, quoteErr := readPriceQuote(tx, r.QuoteID)
		if quoteErr != nil || quote == nil {
			return nil, fmt.Errorf("quote missing")
		}
		if err = quoteCopyable(*quote); err != nil {
			return nil, err
		}
		if _, err = quoteConversion(*quote, r.USDPerUnit); err != nil {
			return nil, err
		}
		if err = validateQuoteTarget(tx, scope, key, *quote); err != nil {
			return nil, err
		}
	}
	if scope == "member" {
		var id int64
		if err = tx.QueryRow("SELECT id FROM route_members WHERE id=?", key).Scan(&id); err != nil {
			return nil, fmt.Errorf("member missing")
		}
	}
	old, err := readBillingRule(tx, scope, key)
	if err != nil {
		return nil, err
	}
	rev := int64(0)
	if old != nil {
		rev = old.Revision
	}
	if expected != rev {
		return nil, ErrPriceConflict
	}
	r.Revision = rev + 1
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if rev == 0 {
		_, err = tx.Exec("INSERT INTO billing_rules(scope,scope_key,revision,payload,updated_at) VALUES(?,?,?,?,?)", scope, key, r.Revision, string(raw), r.UpdatedAt)
	} else {
		var res sql.Result
		res, err = tx.Exec("UPDATE billing_rules SET revision=?,payload=?,updated_at=? WHERE scope=? AND scope_key=? AND revision=?", r.Revision, string(raw), r.UpdatedAt, scope, key, rev)
		if err == nil {
			var n int64
			n, err = res.RowsAffected()
			if err == nil && n != 1 {
				err = ErrPriceConflict
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &r, nil
}

func savePriceQuote(ex sqlExecutor, q domain.PriceQuoteSnapshot) (int64, error) {
	if q.ScopeKey == "" || len(q.ScopeKey) > 1024 || q.Model == "" || len(q.Model) > 512 || len(q.Raw) > 8192 {
		return 0, fmt.Errorf("invalid quote identity")
	}
	if q.Purpose != "reference" && q.Purpose != "upstream" {
		return 0, fmt.Errorf("invalid quote purpose")
	}
	if q.ObservedAt == "" {
		q.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	q.ID = 0
	raw, err := json.Marshal(q)
	if err != nil {
		return 0, err
	}
	res, err := ex.Exec("INSERT INTO price_quotes(scope_key,source,purpose,model,site_id,account_id,upstream_group,observed_at,expires_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?)", q.ScopeKey, q.Source, q.Purpose, q.Model, q.SiteID, q.AccountID, q.UpstreamGroup, q.ObservedAt, q.ExpiresAt, string(raw))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (db *DB) SavePriceQuote(q domain.PriceQuoteSnapshot) (int64, error) {
	if err := q.Validate(); err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := savePriceQuote(tx, q)
	if err != nil {
		return 0, err
	}
	// Bound observation history without deleting quotes referenced by a rule.
	_, err = tx.Exec(`DELETE FROM price_quotes WHERE scope_key=? AND id NOT IN (SELECT id FROM price_quotes WHERE scope_key=? ORDER BY id DESC LIMIT 20) AND id NOT IN (SELECT COALESCE(json_extract(payload,'$.quote_id'),0) FROM billing_rules)`, q.ScopeKey, q.ScopeKey)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func readPriceQuote(ex sqlExecutor, id int64) (*domain.PriceQuoteSnapshot, error) {
	var raw string
	err := ex.QueryRow("SELECT payload FROM price_quotes WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var q domain.PriceQuoteSnapshot
	err = json.Unmarshal([]byte(raw), &q)
	q.ID = id
	return &q, err
}
func (db *DB) PriceQuote(id int64) (*domain.PriceQuoteSnapshot, error) {
	return readPriceQuote(db.DB, id)
}
func (db *DB) PriceQuotes(model string) ([]domain.PriceQuoteSnapshot, error) {
	rows, err := db.Query("SELECT q.id,q.payload FROM price_quotes q WHERE (?='' OR q.model=?) AND q.id=(SELECT MAX(p.id) FROM price_quotes p WHERE p.scope_key=q.scope_key) ORDER BY q.id DESC LIMIT 500", model, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PriceQuoteSnapshot{}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var q domain.PriceQuoteSnapshot
		if err = json.Unmarshal([]byte(raw), &q); err != nil {
			return nil, err
		}
		q.ID = id
		out = append(out, q)
	}
	return out, rows.Err()
}

// ResolveBillingLayer is shared by the relay and the public price preview.
// An explicit inherit suppresses that scope's legacy columns; free stops fallback.
func (db *DB) ResolveBillingLayer(model, upstream string, memberID int64) (domain.PriceLayer, bool, error) {
	if memberID > 0 {
		key := strconv.FormatInt(memberID, 10)
		r, err := db.BillingRule("member", key)
		if err != nil {
			return domain.PriceLayer{}, false, err
		}
		if r != nil {
			if r.State != "inherit" {
				return r.Layer("member", key), true, nil
			}
		} else {
			l, found, err := db.RouteMember.MemberPricing(memberID)
			if err != nil {
				return l, false, err
			}
			if found && l.Priced() {
				l.State = "legacy"
				l.Owner = "member:" + key
				l.Source = "legacy"
				return l, true, nil
			}
		}
	}
	for i, name := range []string{model, upstream} {
		if name == "" || i == 1 && name == model {
			continue
		}
		r, err := db.BillingRule("model", name)
		if err != nil {
			return domain.PriceLayer{}, false, err
		}
		if r != nil {
			if r.State != "inherit" {
				return r.Layer("model", name), true, nil
			}
			continue
		}
		m, err := db.ModelMetadata.Get(name)
		if err != nil {
			return domain.PriceLayer{}, false, err
		}
		if m == nil {
			continue
		}
		l, err := domain.ResolvePriceLayer(m.PricePromptPer1k, m.PriceCompletionPer1k, m.PriceCachePer1k, m.PricePerRequest, m.PriceTiers, m.PriceSchedule)
		if err != nil {
			return l, false, err
		}
		if l.Priced() {
			l.State = "legacy"
			l.Owner = "model:" + name
			l.Source = "legacy"
			return l, true, nil
		}
	}
	return domain.PriceLayer{}, false, nil
}

func quoteConversion(q domain.PriceQuoteSnapshot, usdPerUnit *float64) (float64, error) {
	if q.Unit.Kind == "unknown" {
		return 0, fmt.Errorf("unknown quote unit cannot be copied")
	}
	if q.Status != "calculable" {
		return 0, fmt.Errorf("quote is not calculable")
	}
	if q.ExpiresAt != "" {
		expiry, err := time.Parse(time.RFC3339Nano, q.ExpiresAt)
		if err != nil || !time.Now().Before(expiry) {
			return 0, fmt.Errorf("quote expired")
		}
	}
	if q.Unit.Kind == "iso" && strings.EqualFold(q.Unit.Code, "USD") {
		return 1, nil
	}
	if usdPerUnit == nil || *usdPerUnit <= 0 || math.IsNaN(*usdPerUnit) || math.IsInf(*usdPerUnit, 0) || *usdPerUnit > 1e9 {
		return 0, fmt.Errorf("declare USD per native unit before copying this quote")
	}
	return *usdPerUnit, nil
}

// CopyQuoteRule copies exactly one immutable quote, under a latest-version gate.
// Future observations do not update the resulting billing rule.
// A quote is copyable when the operator could rely on it: it must describe a
// calculable price and must not have expired. "Is it the newest row" is
// deliberately not part of this check — the copied payload is immutable, the
// confirmation shows the exact values, and a background refresh must not turn a
// reviewed quote into a conflict.
func quoteCopyable(q domain.PriceQuoteSnapshot) error {
	if q.Status != "calculable" {
		return fmt.Errorf("quote is not calculable: %s", q.Status)
	}
	if q.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339Nano, q.ExpiresAt); err == nil && !expires.After(time.Now()) {
			return ErrPriceConflict
		}
	}
	return nil
}

func (db *DB) CopyQuoteRule(scope, key string, quoteID, expected int64, usdPerUnit *float64) (*domain.BillingRule, error) {
	q, err := db.PriceQuote(quoteID)
	if err != nil || q == nil {
		return nil, fmt.Errorf("quote missing")
	}
	if err = quoteCopyable(*q); err != nil {
		return nil, err
	}
	factor, err := quoteConversion(*q, usdPerUnit)
	if err != nil {
		return nil, err
	}
	if scope == "member" {
		var siteID int64
		var model, memberMap, routeMap string
		err = db.QueryRow("SELECT COALESCE(c.site_id,0),r.model_pattern,rm.mapping_json,r.mapping_json FROM route_members rm JOIN channels c ON c.id=rm.channel_id JOIN routes r ON r.id=rm.route_id WHERE rm.id=?", key).Scan(&siteID, &model, &memberMap, &routeMap)
		if err != nil {
			return nil, err
		}
		actual := domain.ResolveUpstreamModel(model, memberMap, routeMap)
		if q.Model != actual && q.Model != model {
			return nil, fmt.Errorf("quote model does not match member")
		}
		if q.SiteID > 0 && q.SiteID != siteID {
			return nil, fmt.Errorf("quote belongs to another site")
		}
	} else if q.Model != key {
		return nil, fmt.Errorf("quote model mismatch")
	}
	scale := func(v *float64, div float64) *float64 {
		if v == nil {
			return nil
		}
		n := *v * factor / div
		return &n
	}
	mode := q.Values.Mode
	if mode == "fixed" {
		mode = "request"
	}
	r := domain.BillingRule{State: "explicit", Mode: mode, Input: scale(q.Values.Input, 1000), Output: scale(q.Values.Output, 1000), CacheRead: scale(q.Values.CacheRead, 1000), CacheWrite: scale(q.Values.CacheWrite, 1000), PerRequest: scale(q.Values.PerRequest, 1), Timezone: "UTC", Source: "quote_copy", QuoteID: quoteID, USDPerUnit: usdPerUnit}
	return db.SaveBillingRule(scope, key, r, expected)
}

func validateQuoteTarget(ex sqlExecutor, scope, key string, q domain.PriceQuoteSnapshot) error {
	if scope == "model" {
		if key != q.Model {
			return fmt.Errorf("quote model mismatch")
		}
		return nil
	}
	var siteID int64
	var model, memberMap, routeMap, group string
	err := ex.QueryRow(`SELECT COALESCE(c.site_id,0),r.model_pattern,rm.mapping_json,r.mapping_json,c.group_name FROM route_members rm JOIN channels c ON c.id=rm.channel_id JOIN routes r ON r.id=rm.route_id WHERE rm.id=?`, key).Scan(&siteID, &model, &memberMap, &routeMap, &group)
	if err != nil {
		return err
	}
	if q.Model != domain.ResolveUpstreamModel(model, memberMap, routeMap) && q.Model != model {
		return fmt.Errorf("quote model mismatch")
	}
	if q.SiteID > 0 && q.SiteID != siteID {
		return fmt.Errorf("quote belongs to another site")
	}
	if q.UpstreamGroup != "" && q.UpstreamGroup != normalizeGroupName(group) {
		return fmt.Errorf("quote group mismatch")
	}
	if q.AccountID > 0 {
		var accountID int64
		if err = ex.QueryRow(`SELECT COALESCE((SELECT a.id FROM credentials a WHERE a.id=c.credential_id AND a.site_id=c.site_id AND lower(trim(a.kind)) IN ('access_token','session') AND a.status='enabled' AND (length(a.secret_enc)>0 OR length(a.cookie_enc)>0)),(SELECT a.id FROM credentials a WHERE a.site_id=c.site_id AND lower(trim(a.kind)) IN ('access_token','session') AND a.status='enabled' AND (length(a.secret_enc)>0 OR length(a.cookie_enc)>0) ORDER BY a.id LIMIT 1),0) FROM route_members rm JOIN channels c ON c.id=rm.channel_id WHERE rm.id=?`, key).Scan(&accountID); err != nil {
			return err
		}
		if accountID != q.AccountID {
			return fmt.Errorf("quote account mismatch")
		}
	}
	return nil
}
