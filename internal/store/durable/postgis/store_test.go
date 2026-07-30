package postgis

import "testing"

func TestGeometryJSONExtractsFeatureGeometry(t *testing.T) {
	geometry, err := geometryJSON(`{
		"type":"Feature",
		"properties":{"name":"test"},
		"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if geometry != `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}` {
		t.Fatalf("geometry = %s", geometry)
	}
}

func TestGeometryJSONRejectsUnsupportedGeometry(t *testing.T) {
	if _, err := geometryJSON(`{"type":"Point","coordinates":[0,0]}`); err == nil {
		t.Fatal("expected unsupported geometry error")
	}
}
