package validation

import (
	"regexp"
	"strings"
)

// matchKey is the canonical (method, path) key used for the schema↔consumer
// outer join. Methods are uppercased, paths are normalized so {orgID} and
// {orgId} hash the same way.
type matchKey struct {
	Method string
	Path   string
}

// matchResult is the output of comparing schema endpoints against consumer
// endpoints. Three explicit categories — no information loss.
type matchResult struct {
	SchemaOnly   []schemaEndpoint
	ConsumerOnly []consumerEndpoint
	Matched      []endpointMatch
}

// endpointMatch describes one endpoint that exists in both schema and a
// consumer. The consumer slice can hold both meshery and meshery-cloud rows
// when the same path is implemented in both repos.
type endpointMatch struct {
	Schema    schemaEndpoint
	Consumers []consumerEndpoint
}

// fieldDiff describes a single field discrepancy between schema and consumer.
type fieldDiff struct {
	FieldName    string
	InSchema     bool
	InConsumer   bool
	SchemaType   string
	ConsumerType string
}

var paramRE = regexp.MustCompile(`\{([^}]+)\}`)

// normalizeMatchKey produces the canonical match key for a (method, path)
// tuple. The display value of the path is preserved on the original
// schemaEndpoint / consumerEndpoint — only the lookup key is normalized.
func normalizeMatchKey(method, path string) matchKey {
	method = strings.ToUpper(strings.TrimSpace(method))
	if path == "" {
		return matchKey{Method: method}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	path = paramRE.ReplaceAllStringFunc(path, func(m string) string {
		inner := m[1 : len(m)-1]
		return "{" + strings.ToLower(inner) + "}"
	})
	return matchKey{Method: method, Path: path}
}

// matchEndpoints performs the full outer join described in section 9 of the
// architecture doc.
func matchEndpoints(schema *schemaIndex, mesheryConsumers, cloudConsumers []consumerEndpoint) *matchResult {
	result := &matchResult{}
	if schema == nil {
		schema = &schemaIndex{}
	}

	mesheryByKey := make(map[matchKey][]int, len(mesheryConsumers))
	for i, ep := range mesheryConsumers {
		k := normalizeMatchKey(ep.Method, ep.Path)
		mesheryByKey[k] = append(mesheryByKey[k], i)
	}
	cloudByKey := make(map[matchKey][]int, len(cloudConsumers))
	for i, ep := range cloudConsumers {
		k := normalizeMatchKey(ep.Method, ep.Path)
		cloudByKey[k] = append(cloudByKey[k], i)
	}

	usedMeshery := make(map[int]bool, len(mesheryConsumers))
	usedCloud := make(map[int]bool, len(cloudConsumers))

	for _, ep := range schema.Endpoints {
		key := normalizeMatchKey(ep.Method, ep.Path)
		// Apply x-internal filter for join.
		mesheryAllowed := xInternalAllows(ep.XInternal, "meshery")
		cloudAllowed := xInternalAllows(ep.XInternal, "cloud")

		var consumers []consumerEndpoint
		if mesheryAllowed {
			for _, i := range mesheryByKey[key] {
				consumers = append(consumers, mesheryConsumers[i])
				usedMeshery[i] = true
			}
		}
		// ANY method matching: a consumer registered with method "ANY"
		// implements every verb on that path.
		if mesheryAllowed {
			anyKey := matchKey{Method: "ANY", Path: key.Path}
			for _, i := range mesheryByKey[anyKey] {
				consumers = append(consumers, mesheryConsumers[i])
				usedMeshery[i] = true
			}
		}
		if cloudAllowed {
			for _, i := range cloudByKey[key] {
				consumers = append(consumers, cloudConsumers[i])
				usedCloud[i] = true
			}
			anyKey := matchKey{Method: "ANY", Path: key.Path}
			for _, i := range cloudByKey[anyKey] {
				consumers = append(consumers, cloudConsumers[i])
				usedCloud[i] = true
			}
		}

		if len(consumers) == 0 {
			result.SchemaOnly = append(result.SchemaOnly, ep)
			continue
		}
		result.Matched = append(result.Matched, endpointMatch{
			Schema:    ep,
			Consumers: consumers,
		})
	}

	for i, ep := range mesheryConsumers {
		if !usedMeshery[i] {
			result.ConsumerOnly = append(result.ConsumerOnly, ep)
		}
	}
	for i, ep := range cloudConsumers {
		if !usedCloud[i] {
			result.ConsumerOnly = append(result.ConsumerOnly, ep)
		}
	}

	return result
}

// xInternalAllows returns true if a schema endpoint with the given x-internal
// list is meant to be implemented by the named repo.
func xInternalAllows(xInternal []string, repo string) bool {
	if len(xInternal) == 0 {
		return true
	}
	for _, target := range xInternal {
		if target == repo {
			return true
		}
	}
	return false
}

// classifySchemaBacked returns the Schema-Backed value for a given match.
//   - TRUE: schema endpoint exists with a 2xx response that has a $ref schema
//   - Partial: schema endpoint exists but no 2xx $ref
//   - FALSE: no schema endpoint
func classifySchemaBacked(schemaPresent bool, ep schemaEndpoint) string {
	if !schemaPresent {
		return "FALSE"
	}
	if ep.HasSuccessRef {
		return "TRUE"
	}
	return "Partial"
}

// classifySchemaDriven returns the Schema-Driven value for the given consumer
// endpoint and the matched schema's request/response shapes. The contract is
// stricter than file-level import detection: TRUE requires that we
// successfully verified at least one of the request or response shapes
// against the consumer's actually-inspected Go type, with no field diffs.
//
//   - N/A:         consumer repo not provided / no consumer endpoint
//   - Not Audited: handler unresolved or marked anonymous
//   - FALSE:       handler does not import meshery/schemas
//   - TRUE:        handler imports schemas AND at least one shape was
//     verified successfully (no diffs) AND no shape verification produced
//     diffs
//   - Partial:     handler imports schemas but verification either produced
//     diffs OR could not be performed for any shape (e.g. body-scan didn't
//     find a usable type). This is the honest fallback when we know the
//     file imports schemas but cannot prove conformance.
//
// This classification is intentionally conservative: if inspection could not
// prove schema conformance, the result falls back to Partial instead of TRUE.
func classifySchemaDriven(consumerProvided bool, c *consumerEndpoint, requestShape, responseShape *schemaShape) string {
	if !consumerProvided {
		return "N/A"
	}
	if c == nil {
		return "Not Audited"
	}
	if c.HandlerName == "" || c.HandlerName == "(anonymous)" {
		return "Not Audited"
	}
	if c.HandlerFile == "" {
		return "Not Audited"
	}
	if !c.ImportsSchemas {
		return "FALSE"
	}

	reqStatus := verifyShape(requestShape, c.RequestType, true)
	respStatus := verifyShape(responseShape, c.ResponseType, false)

	// Any concrete diff downgrades the result to Partial — even if the
	// other side verified cleanly.
	if reqStatus == shapeDiff || respStatus == shapeDiff {
		return "Partial"
	}
	// At least one side must have been successfully verified.
	if reqStatus == shapeOK || respStatus == shapeOK {
		return "TRUE"
	}
	// Neither side could be verified. We have a positive import signal but
	// no field-level proof — Partial is the honest answer.
	return "Partial"
}

// shapeStatus is the per-side outcome of verifyShape.
type shapeStatus int

const (
	// shapeUnverified means we don't have enough information to compare
	// (no schema shape, no consumer type info, or no inspected fields).
	shapeUnverified shapeStatus = iota
	// shapeOK means we compared schema and consumer fields and found no
	// material diffs.
	shapeOK
	// shapeDiff means we compared and found at least one diff.
	shapeDiff
)

// verifyShape compares one schema shape against the corresponding consumer
// type info and reports whether the comparison succeeded, failed, or could
// not be performed.
//
// A "successful" verification requires that the consumer side has a
// non-empty Fields map. The current handler-body scanner usually can't
// populate Fields, so most calls return shapeUnverified — that is the
// truthful state of the analysis pipeline today.
func verifyShape(shape *schemaShape, info *goTypeInfo, requestSide bool) shapeStatus {
	if shape == nil || info == nil {
		return shapeUnverified
	}
	if len(info.Fields) == 0 {
		return shapeUnverified
	}
	if len(diffFields(shape, info, requestSide)) > 0 {
		return shapeDiff
	}
	return shapeOK
}

// diffFields compares a schema shape against a Go type's field set. When
// requestSide is true, server-generated fields like id/created_at are
// allowed to be missing from the consumer struct.
func diffFields(shape *schemaShape, info *goTypeInfo, requestSide bool) []fieldDiff {
	if shape == nil || info == nil {
		return nil
	}
	var diffs []fieldDiff
	for name, fs := range shape.Fields {
		if requestSide && (serverGeneratedFields[name] || dbMirroredFields[name]) {
			continue
		}
		consumerType, ok := info.Fields[name]
		if !ok {
			diffs = append(diffs, fieldDiff{
				FieldName:  name,
				InSchema:   true,
				InConsumer: false,
				SchemaType: fs.Type,
			})
			continue
		}
		if !typesCompatible(fs.Type, consumerType) {
			diffs = append(diffs, fieldDiff{
				FieldName:    name,
				InSchema:     true,
				InConsumer:   true,
				SchemaType:   fs.Type,
				ConsumerType: consumerType,
			})
		}
	}
	for name, ct := range info.Fields {
		if _, ok := shape.Fields[name]; ok {
			continue
		}
		if requestSide && (serverGeneratedFields[name] || dbMirroredFields[name]) {
			continue
		}
		diffs = append(diffs, fieldDiff{
			FieldName:    name,
			InSchema:     false,
			InConsumer:   true,
			ConsumerType: ct,
		})
	}
	return diffs
}

// typesCompatible relaxes the comparison between OpenAPI scalar names and Go
// type names ("integer" ↔ "int", "boolean" ↔ "bool", etc.).
func typesCompatible(openapiType, goType string) bool {
	if openapiType == "" || goType == "" {
		return true
	}
	openapiType = strings.ToLower(openapiType)
	goType = strings.ToLower(strings.TrimPrefix(goType, "*"))
	switch openapiType {
	case "string":
		return strings.Contains(goType, "string") || strings.Contains(goType, "uuid") ||
			strings.Contains(goType, "time") || strings.Contains(goType, "byte")
	case "integer":
		return strings.HasPrefix(goType, "int") || strings.HasPrefix(goType, "uint")
	case "number":
		return strings.HasPrefix(goType, "float") || strings.HasPrefix(goType, "int")
	case "boolean":
		return goType == "bool"
	case "array":
		return strings.HasPrefix(goType, "[]")
	case "object":
		return strings.Contains(goType, "map") || strings.Contains(goType, "struct") || !isPrimitive(goType)
	}
	return openapiType == goType
}

func isPrimitive(goType string) bool {
	switch goType {
	case "string", "bool", "byte", "rune",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	return false
}
