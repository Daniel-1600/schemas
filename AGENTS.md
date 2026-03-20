# Meshery Schemas — Coding Agent Instructions

This is the central schema repository for the Meshery platform. Schemas here drive Go struct generation, TypeScript type generation, and RTK Query client generation. Mistakes in schema design propagate into generated code across multiple downstream repos (meshery/meshery, layer5io/meshery-cloud).

## Build

```bash
make build       # generate Go structs + TypeScript types + RTK clients
npm run build    # build TypeScript distribution (dist/)
```

Generated files (`models/`, `typescript/generated/`, `dist/`) are NOT committed. Only source schemas and manually written helper files are committed.

## The Dual-Schema Pattern (REQUIRED)

This is the most critical design rule in this repo. Every agent or contributor MUST follow it.

### Rule 1: `<construct>.yaml` = response schema only

The YAML file for an entity represents the **full server-side object** as returned in API responses. It is NOT a request body schema.

**Required properties of every entity `.yaml`:**
- `additionalProperties: false` at the top level
- All server-generated fields defined in `properties`: `id`, `created_at`, `updated_at`, `deleted_at`
- Server-generated fields that are always present belong in `required`

```yaml
# CORRECT: keychain.yaml
type: object
additionalProperties: false
required:
  - id
  - name
  - owner
  - created_at
  - updated_at
properties:
  id:
    $ref: ../../v1alpha1/core/api.yml#/components/schemas/uuid
  name:
    type: string
  owner:
    $ref: ../../v1alpha1/core/api.yml#/components/schemas/uuid
  created_at:
    $ref: ../../v1alpha1/core/api.yml#/components/schemas/created_at
  updated_at:
    $ref: ../../v1alpha1/core/api.yml#/components/schemas/updated_at
  deleted_at:
    $ref: ../../v1alpha1/core/api.yml#/components/schemas/nullTime
```

### Rule 2: Every writable entity needs a `*Payload` schema in `api.yml`

For any entity that has `POST` or `PUT` operations, define a `{Construct}Payload` schema in `api.yml` that:
- Contains **only client-settable fields** — no `created_at`, `updated_at`, `deleted_at`
- Makes `id` optional with `json:"id,omitempty"` for upsert patterns
- Is the schema referenced by all `requestBody` entries for `POST`/`PUT`

```yaml
# CORRECT: in api.yml
components:
  schemas:
    KeychainPayload:
      type: object
      description: Payload for creating or updating a keychain.
      required:
        - name
      properties:
        id:
          $ref: ../../v1alpha1/core/api.yml#/components/schemas/uuid
          description: Existing keychain ID for updates; omit on create.
          x-oapi-codegen-extra-tags:
            json: "id,omitempty"
        name:
          type: string
        owner:
          $ref: ../../v1alpha1/core/api.yml#/components/schemas/uuid
          x-oapi-codegen-extra-tags:
            json: "owner,omitempty"
```

### Rule 3: POST/PUT requestBody MUST reference `*Payload`, never the entity schema

```yaml
# WRONG — forces clients to supply server-generated fields
post:
  requestBody:
    content:
      application/json:
        schema:
          $ref: "#/components/schemas/Keychain"

# CORRECT — separate payload type for writes
post:
  requestBody:
    content:
      application/json:
        schema:
          $ref: "#/components/schemas/KeychainPayload"
```

### Canonical reference implementations

When uncertain, model new schemas on these:
- `schemas/constructs/v1beta1/connection/` — `connection.yaml` + `ConnectionPayload` in `api.yml`
- `schemas/constructs/v1beta1/key/` — `key.yaml` + `KeyPayload` in `api.yml`
- `schemas/constructs/v1beta1/team/` — `team.yaml` + `teamPayload`/`teamUpdatePayload` in `api.yml`
- `schemas/constructs/v1beta1/environment/` — `environment.yaml` + `environmentPayload` in `api.yml`

## Checklist for new entity schemas

Before opening a PR, verify:
- [ ] `<construct>.yaml` has `additionalProperties: false`
- [ ] `<construct>.yaml` lists all server-generated fields in `properties` and appropriate ones in `required`
- [ ] `api.yml` defines `{Construct}Payload` with only client-settable fields
- [ ] All `POST`/`PUT` `requestBody` entries reference `{Construct}Payload`
- [ ] `GET` responses reference the full `{Construct}` entity schema

## Naming conventions

- Property names: `camelCase` (`schemaVersion`, `displayName`)
- ID-suffix fields: `lowerCamelCase` + `Id` (`modelId`, `registrantId`)
- Enums: lowercase words (`enabled`, `ignored`, `duplicate`)
- Object names: singular nouns (`model`, `component`, `design`)
- `components/schemas` names: PascalCase nouns (`Model`, `Component`, `KeychainPayload`)
- Files/folders: lowercase (`api.yml`, `keychain.yaml`, `templates/keychain_template.json`)
- Endpoint paths: `/api` prefix, kebab-case, plural nouns (`/api/workspaces`, `/api/environments`)
- Path params: camelCase (`{subscriptionId}`, `{connectionId}`)
- `operationId`: camelCase VerbNoun (`createKeychain`, `updateEnvironment`)

## File structure for a construct

```
schemas/constructs/v1beta1/<construct>/
  api.yml                          # OpenAPI spec: endpoints + all schema definitions
  <construct>.yaml                 # Entity (response) schema
  templates/
    <construct>_template.json      # Example instance
    <construct>_template.yaml
```

## Go helper files

Auto-generated Go structs (`models/<version>/<construct>/<construct>.go`) are NOT committed. Manually written helpers ARE committed:

```
models/v1beta1/<construct>/
  <construct>.go          # Auto-generated — DO NOT edit
  <construct>_helper.go   # Manual — add SQL driver, Entity interface, TableName(), etc.
```

Always add `// This is not autogenerated.` at the top of helper files.

Use `x-generate-db-helpers: true` on a schema component to auto-generate `Scan`/`Value` SQL driver methods for types stored as JSON blobs in a single DB column. Do NOT use this for types mapped to full DB tables.

## x-internal annotation

Control which bundled output includes an API path:
- `x-internal: ["cloud"]` — cloud-only (`cloud_schema.yml`)
- `x-internal: ["meshery"]` — Meshery-only (`meshery_schema.yml`)
- Omit `x-internal` — included in all bundles
