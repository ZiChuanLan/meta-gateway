# Availability experiment

Base: `79b722d72caed408703cbc498a43dcba71a214d5`. Branch: `experiment/availability`. Local clone; source push URL disabled.

Scope: move site observation to Connections; list every collected observation; add source-labelled availability histories to channel-model lists, route members and route summaries; fix attribution, alias/group matching, stale data and duplicate heartbeats. Preserve manual/fixed routing and billing semantics. Page reads never launch probes. Complete this feature first, then inspect and improve neighboring UI/feature problems. Verify classic/modern, light/dark, desktop/mobile, HTTP collection and relay paths. Original repository edits, commits and push require approval of the complete preview.

Verification: real-path regression tests, CI web gates, Go quality gates, docsgen, race, Compose when available, and an isolated gateway with deterministic mock upstreams. Record external limitations exactly.