# Copilot Review Instructions for meshery/schemas

## Intentional Design Decisions (Do Not Flag)

The following patterns are intentional and should not be flagged as issues during code review:

### 1. `SqlNullTime` vs `NullTime`

Some entities (e.g., `Subscription.deleted_at`) use `SqlNullTime` instead of `NullTime`. This is intentional for backward compatibility with v1beta1 entities and downstream GORM/Pop consumers. Do not suggest switching to `NullTime` unless the entire entity is being migrated.

### 2. Unversioned `models/core` import path

Go type imports referencing `github.com/meshery/schemas/models/core` (without a version segment) are correct. The `models/core/` directory is the canonical Go package for shared types (`Map`, `NullTime`, etc.) and is deliberately not version-scoped. Do not suggest adding a version prefix like `models/v1beta2/core`.

### 3. `x-enum-casing-exempt: true`

Enum schemas annotated with `x-enum-casing-exempt: true` contain published enum values that will never be lowercased (e.g., `PlanName`: `"Free"`, `"Team Designer"`; `FeatureName`: `"ComponentsInDesign"`). Do not suggest lowercasing these values.

### 4. `page_size` and `total_count` in pagination envelopes

Pagination envelope fields use `page_size` and `total_count` (snake_case) as a published API contract. These are NOT database-backed fields — the snake_case is a deliberate exception. Do not suggest renaming to `pageSize`/`totalCount`.

### 5. Deprecated v1beta1 constructs

Files with `x-deprecated: true` in their `info` section are intentionally kept for backward compatibility. They contain known style/casing violations that are fixed in the v1beta2 replacement. Do not flag issues in deprecated constructs.

### 6. `deleted_at` in entity `required` lists

Some entity schemas (e.g., `AcademyCurricula`) list `deleted_at`/`deletedAt` as required. This is intentional — server-generated fields that are always present in API responses belong in `required` per AGENTS.md, even when the value is null for non-deleted resources.

## Schema Validation Rules

This repository enforces 33 schema validation rules via `build/validate-schemas.js`. For full details, see:
- `AGENTS.md` — casing rules, HTTP design principles, dual-schema pattern
- `specs/casing-rules.md` — definitive casing reference with ORM implications
- `build/validate-schemas.js` — rule implementations (header comments document each rule)

Key casing rules:
- DB-backed fields: exact snake_case matching the database column name
- Non-DB fields: camelCase
- Schema component names: PascalCase
- operationId: lower camelCase verbNoun
- Path segments: kebab-case
- Path parameters: camelCase with `Id` suffix
