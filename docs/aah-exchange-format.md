# AAH Exchange Format

Meta Gateway provides authenticated JSON import and export at:

- `POST /admin/exchange/export`
- `POST /admin/exchange/import`

Both endpoints require `Authorization: Bearer <ADMIN_TOKEN>`. The current
canonical format name is `meta-gateway-aah-exchange`, version `1`.

## Canonical Envelope

```json
{
  "format": "meta-gateway-aah-exchange",
  "version": 1,
  "exported_at": "2026-07-14T00:00:00Z",
  "importable": true,
  "items": [
    {
      "name": "primary",
      "base_url": "https://api.example.com",
      "api_key": "secret",
      "models": ["model-a"],
      "group": "default",
      "priority": 0,
      "weight": 100,
      "site_type_hint": "openai-compatible"
    }
  ]
}
```

Canonical input is strict: every envelope and item field above is required,
unknown fields are rejected, `version` must be `1`, and `importable` must be
`true`. `exported_at` must be RFC3339. Metadata-only exports omit `api_key`, set
`importable` to `false`, and cannot be imported.

## Export

Export all channels without secrets:

```json
{ "include_secrets": false }
```

Export selected channels as a portable secret-bearing document:

```json
{ "include_secrets": true, "channel_ids": [1, 2] }
```

`channel_ids` is optional and must contain unique positive integers. Selection
is all-or-nothing: a missing requested channel returns `404`. Output items are
ordered by channel ID. Secret-bearing responses include
`Cache-Control: no-store`; internal AES-GCM ciphertext and import fingerprints
are never exported.

Treat a portable export as plaintext credential material. Store and transmit it
only through an appropriately protected channel, then delete it when no longer
needed. Metadata export is the safer choice for inventory and review.

## Compatible Imports

Import accepts exactly three input families:

1. The canonical version 1 envelope.
2. A New API channel array, `{ "channels": [...] }`, or `{ "data": [...] }`.
   Documented aliases include `key`/`api_key`/`apiKey`,
   `base_url`/`baseUrl`, and type hint aliases. Models and groups may be CSV
   strings or string arrays.
3. An All API Hub V2 backup with `version: "2.0"` and
   `apiCredentialProfiles.profiles`.

AAH profile imports use models `[]`, group `default`, priority `0`, weight
`100`, enabled status, and normalized `apiType` or `openai-compatible` as the
type hint. Accounts, preferences, WebDAV data, `channelConfigs`, notes, and
other backup sections are ignored and never restored.

## Validation And Normalization

- The body limit is 10 MiB and trailing JSON is rejected.
- URLs must be absolute HTTP(S), include a host, and contain no userinfo, query,
  or fragment. Scheme and host are lowercased, default ports and trailing
  slashes are removed, and paths are cleaned.
- Names, URLs, and API keys are required. Length, item-count, priority, weight,
  status, and list bounds are enforced.
- Compatibility defaults apply only when an optional field is absent. A field
  that is present with the wrong type, or conflicting values supplied through
  multiple aliases, is rejected.
- Models and groups are trimmed, deduplicated, and sorted.
- Duplicate normalized URL plus API-key identities in one document are
  rejected before any database transaction starts.
- Ambiguous root shapes and unsupported versions return `422`.

## Identity And Transactions

Idempotency uses a purpose-separated HMAC-SHA256 fingerprint over normalized
base URL, a NUL separator, and API key. Plaintext keys, unsalted key hashes, and
random AES-GCM ciphertext are not used as persisted identity.

The same URL and key updates one imported channel. The same URL with another
key creates another dedicated credential/channel under the same site. A unique
matching pre-exchange asset can be adopted after decryption and constant-time
key comparison; shared or multiple matching legacy assets return `409`.

Every request validates fully before writing. Site, Credential, and Channel
changes commit in one SQLite transaction, so a persistence failure leaves no
partial import. After commit, affected channels are refreshed exactly once in
ascending ID order through the existing discovery service. Discovery failures
are returned as redacted item results and do not roll back imported assets.
Manual route members and manual overrides remain operator-owned.

## Import Response

```json
{
  "created_count": 1,
  "updated_count": 0,
  "adopted_count": 0,
  "channel_ids": [3],
  "discovery": [
    { "channel_id": 3, "status": "success" }
  ],
  "discovery_success_count": 1,
  "discovery_failure_count": 0
}
```

Responses contain no credentials, ciphertext, raw upstream bodies, or database
details. Stable errors are `validation_error` (`400`), `channel_not_found`
(`404`), `identity_conflict` (`409`), `body_too_large` (`413`),
`unsupported_format` (`422`), and `internal_error` (`500`).
