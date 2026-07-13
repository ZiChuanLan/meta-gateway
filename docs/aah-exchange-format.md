# AAH Exchange Format (Stub)

> **Status:** Placeholder for P6 implementation.
> This document describes the intended exchange format between Meta Gateway instances (and compatible systems).

## Overview

The AAH (All API Hub) Exchange Format is a versioned JSON interchange format for sharing channel configurations between gateways. It allows exporting channels from one Meta Gateway instance and importing them into another.

## Format

```json
{
  "format": "aah-channel-export",
  "version": 1,
  "exported_at": "2026-07-14T00:00:00Z",
  "source": {
    "name": "gateway-name",
    "version": "0.1.0"
  },
  "items": [
    {
      "name": "channel-display-name",
      "base_url": "https://api.openai.com",
      "api_key": "sk-encrypted-or-plaintext",
      "models": ["gpt-4", "gpt-3.5-turbo"],
      "group": "default",
      "priority": 0,
      "weight": 100,
      "site_type_hint": "openai-compatible"
    }
  ]
}
```

## Fields

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `format` | string | yes | Must be `"aah-channel-export"` |
| `version` | integer | yes | Format version (currently 1) |
| `exported_at` | string (RFC3339) | yes | Export timestamp |
| `source.name` | string | no | Originating gateway name |
| `source.version` | string | no | Originating gateway version |
| `items[].name` | string | yes | Channel display name |
| `items[].base_url` | string | yes | Upstream base URL |
| `items[].api_key` | string | yes | API key (encrypted or plaintext depending on export policy) |
| `items[].models` | array[string] | yes | Model identifiers |
| `items[].group` | string | no | Model group (default: "default") |
| `items[].priority` | integer | no | Priority tier |
| `items[].weight` | integer | no | Weight for load balancing |
| `items[].site_type_hint` | string | no | Upstream type hint |

## Import Pipeline (Planned)

1. Validate schema and version
2. For each item → upsert Channel by `(base_url, api_key)`
3. Encrypt secret at rest
4. Return summary: created/updated/skipped/errors

## Export Pipeline (Planned)

1. Select channels (all or filter by criteria)
2. Decrypt keys only for authorized admin call
3. Emit versioned JSON

## Non-goals

- Full New API admin surface compatibility
- Bidirectional sync (push/pull)
- Real-time channel sharing

This is a pure import/export DTO mechanism, not a management API.
