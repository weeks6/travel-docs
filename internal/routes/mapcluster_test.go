package routes

import (
	"math"
	"testing"
)

func TestMergeClose(t *testing.T) {
	single := func(lon, lat float64, name string) MapCluster {
		return MapCluster{Lon: lon, Lat: lat, Count: 1, BBox: [4]float64{lon, lat, lon, lat}, Route: &MapRoute{Name: name}}
	}
	group := func(lon, lat float64, n int) MapCluster {
		return MapCluster{Lon: lon, Lat: lat, Count: n, BBox: [4]float64{lon - 0.01, lat - 0.01, lon + 0.01, lat + 0.01}}
	}
	const cell = 5000.0 // meters; 0.01° of longitude is ~1.1 km

	// Two grid clusters either side of a cell edge, 0.02° (~2.2 km) apart,
	// and one far away.
	got := mergeClose([]MapCluster{
		group(8.00, 46.0, 24),
		group(8.02, 46.0, 65),
		single(9.00, 46.0, "far"),
	}, cell)
	if len(got) != 2 {
		t.Fatalf("got %d clusters, want 2: %+v", len(got), got)
	}
	m := got[0]
	if m.Count != 89 || m.Route != nil {
		t.Errorf("merged = %+v", m)
	}
	if want := (8.00*24 + 8.02*65) / 89; math.Abs(m.Lon-want) > 1e-9 {
		t.Errorf("center lon = %v, want %v (weighted)", m.Lon, want)
	}
	if m.BBox != [4]float64{7.99, 45.99, 8.03, 46.01} {
		t.Errorf("bbox = %v", m.BBox)
	}
	if got[1].Route == nil || got[1].Route.Name != "far" {
		t.Errorf("far = %+v", got[1])
	}

	// Single routes merge into a group without a route.
	got = mergeClose([]MapCluster{single(8.0, 46.0, "a"), single(8.001, 46.0, "b")}, cell)
	if len(got) != 1 || got[0].Count != 2 || got[0].Route != nil {
		t.Errorf("got %+v", got)
	}

	// Results are at least a cell apart.
	var many []MapCluster
	for i := range 200 {
		many = append(many, single(8+float64(i%20)*0.02, 46+float64(i/20)*0.02, "x"))
	}
	got = mergeClose(many, cell)
	total := 0
	for i, a := range got {
		total += a.Count
		for _, b := range got[i+1:] {
			ax, ay := mercator(a.Lon, a.Lat)
			bx, by := mercator(b.Lon, b.Lat)
			if math.Hypot(ax-bx, ay-by) < cell*0.99 {
				t.Errorf("clusters %v and %v are closer than a cell", a, b)
			}
		}
	}
	if total != 200 {
		t.Errorf("total = %d, want 200", total)
	}
}
