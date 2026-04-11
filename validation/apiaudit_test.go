package validation

import "testing"

// TestComputeSummaryCountsPartial verifies that Partial rows contribute to
// MesheryDrivenPartial / CloudDrivenPartial / SchemaDrivenPartial. The
// maintainer review flagged that the previous summary omitted this bucket
// entirely after classifySchemaDriven started using Partial as its honest
// fallback.
func TestComputeSummaryCountsPartial(t *testing.T) {
	rows := []auditRow{
		{SchemaBacked: "TRUE", SchemaDrivenMeshery: "Partial", SchemaDrivenCloud: "Not Audited"},
		{SchemaBacked: "TRUE", SchemaDrivenMeshery: "TRUE", SchemaDrivenCloud: "Partial"},
		{SchemaBacked: "TRUE", SchemaDrivenMeshery: "FALSE", SchemaDrivenCloud: "Partial"},
		{SchemaBacked: "Partial", SchemaDrivenMeshery: "Partial", SchemaDrivenCloud: "N/A"},
	}
	idx := &schemaIndex{Endpoints: []schemaEndpoint{
		{Method: "GET", Path: "/a"},
		{Method: "GET", Path: "/b"},
		{Method: "GET", Path: "/c"},
		{Method: "GET", Path: "/d"},
	}}
	match := &matchResult{}
	got := computeSummary(idx, nil, nil, match, rows, true, true)

	if got.MesheryDrivenPartial != 2 {
		t.Errorf("MesheryDrivenPartial: got %d, want 2", got.MesheryDrivenPartial)
	}
	if got.CloudDrivenPartial != 2 {
		t.Errorf("CloudDrivenPartial: got %d, want 2", got.CloudDrivenPartial)
	}
	if got.SchemaDrivenPartial != 4 {
		t.Errorf("SchemaDrivenPartial: got %d, want 4", got.SchemaDrivenPartial)
	}
	if got.MesheryDrivenTrue != 1 {
		t.Errorf("MesheryDrivenTrue: got %d, want 1", got.MesheryDrivenTrue)
	}
	if got.MesheryDrivenFalse != 1 {
		t.Errorf("MesheryDrivenFalse: got %d, want 1", got.MesheryDrivenFalse)
	}
}
