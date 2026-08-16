-- 070: single-channel routing mode pin.
-- routing_mode = 'single' pins the route to one route_members row; every other
-- member is skipped at evaluation time and cross-channel retry counts as 0.
ALTER TABLE routes ADD COLUMN single_member_id INTEGER REFERENCES route_members(id) ON DELETE SET NULL;
