"""Give the isolated acceptance stack a real availability reading.

Why this exists: the sites in `docker-compose.audit.yml` are seeded from public
price tables, and a price table publishes no pass/fail verdicts. Every row of
the observation catalogue therefore renders the "no reading" state, and the
availability column cannot be looked at end to end. This writes one heartbeat
history for the mock site, exactly the rows a public status page would have
supplied, so the chip, its detail dialog and the member rows all show a reading.

Nothing here touches a real site: the volume is the acceptance stack's own.

    docker compose -p mg-audit -f docker-compose.audit.yml up -d --build --wait
    docker run --rm -v mg-audit_mg-audit-data:/data \
      -v "$PWD/tools/audit-mock:/seed" python:3.12-alpine python /seed/seed_beats.py

Three failures land between 24h and 72h back, so the default 24-hour view reads
100% while the 7-day view shows the dip in its tick strip.
"""

import datetime
import sqlite3
import sys

DB = "/data/meta-gateway.db"
SITE_ID = 1
FAILED_HOURS_AGO = {30, 55, 70}
HOURS = 24 * 6

con = sqlite3.connect(DB, timeout=30)
cur = con.cursor()

cur.execute("select id, name from sites where id=?", (SITE_ID,))
site = cur.fetchone()
if not site:
    sys.exit(f"site {SITE_ID} not found")
print("site:", site)

# The heartbeat key is the hash of the site's probe source; the samples this
# site already has carry it, so nothing has to be recomputed here.
cur.execute("select distinct source_key from site_probe_samples where site_id=?", (SITE_ID,))
source_key = next((row[0] for row in cur.fetchall() if row[0]), "")
if not source_key:
    sys.exit("no non-empty source_key on this site's samples; run the collector once first")
print("source key:", source_key[:16] + "...")

cur.execute(
    "select distinct monitor_id, group_name from site_probe_samples where site_id=?",
    (SITE_ID,),
)
monitors = cur.fetchall()
if not monitors:
    sys.exit("no samples for this site; run the collector once first")

# This script owns the site's synthetic failures, so a re-run replaces them
# instead of stacking a second dip onto the same window.
cur.execute("delete from site_probe_heartbeats where site_id=? and status=0", (SITE_ID,))
print("removed previous synthetic failures:", cur.rowcount)

cur.execute(
    "select distinct raw_model from site_probe_samples where site_id=? order by raw_model limit 1",
    (SITE_ID,),
)
row = cur.fetchone()
raw_model = row[0] if row else "audit"

now = datetime.datetime.now(datetime.timezone.utc)
beats = []
for hours_ago in range(HOURS):
    at = now - datetime.timedelta(hours=hours_ago)
    status = 0 if hours_ago in FAILED_HOURS_AGO else 1
    stamp = at.strftime("%Y-%m-%d %H:%M:%S")
    for monitor_id, group_name in monitors:
        beats.append(
            (SITE_ID, source_key, monitor_id or "", raw_model, group_name or "", stamp, status, 120)
        )

cur.executemany(
    "insert into site_probe_heartbeats"
    "(site_id,source_key,monitor_id,raw_model,group_name,probed_at,status,ping_ms) "
    "values(?,?,?,?,?,?,?,?) "
    "on conflict(site_id,source_key,monitor_id,probed_at) do update set status=excluded.status",
    beats,
)
con.commit()

for label, hours in (("24h", 24), ("7d", 24 * 7)):
    cur.execute(
        "select count(*), sum(status=1) from site_probe_heartbeats where site_id=? and probed_at>=?",
        (SITE_ID, (now - datetime.timedelta(hours=hours)).strftime("%Y-%m-%d %H:%M:%S")),
    )
    total, ok = cur.fetchone()
    print(f"last {label}: {ok}/{total} ok" + (f" ({ok / total:.1%})" if total else ""))
con.close()
