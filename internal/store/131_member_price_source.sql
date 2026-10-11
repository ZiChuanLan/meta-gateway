-- Which writer owns a member's billing price, and when it last changed.
-- Billing already cascades member price -> model price; this only makes the
-- first layer explainable after the fact.
ALTER TABLE route_members ADD COLUMN price_source TEXT NOT NULL DEFAULT '';
ALTER TABLE route_members ADD COLUMN price_updated_at TEXT NOT NULL DEFAULT '';
