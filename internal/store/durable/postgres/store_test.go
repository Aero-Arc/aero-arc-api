package postgres

import "testing"

func TestGeometryJSONExtractsFeatureGeometry(t *testing.T) {
	got, err := geometryJSON(`{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}` {
		t.Fatalf("geometry = %s", got)
	}
}

func TestGeometryJSONRejectsUnsupportedGeometry(t *testing.T) {
	if _, err := geometryJSON(`{"type":"Point","coordinates":[0,0]}`); err == nil {
		t.Fatal("expected unsupported geometry error")
	}
}
