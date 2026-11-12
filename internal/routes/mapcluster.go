package routes

import (
	"cmp"
	"math"
	"slices"
)

// mergeClose merges clusters whose centers are less than cell meters apart
// in Web Mercator. Grid clustering alone puts points on both sides of a
// cell edge in different clusters, which can then be drawn on top of each
// other; after this pass, cluster centers are at least a cell apart.
//
// Larger clusters absorb smaller ones; the result is ordered largest first.
// Merging moves a cluster's center, which can bring it close to another,
// so passes repeat until nothing merges (each pass that merges shrinks the
// list, so this ends).
func mergeClose(clusters []MapCluster, cell float64) []MapCluster {
	for {
		n := len(clusters)
		clusters = mergePass(clusters, cell)
		if len(clusters) == n {
			return clusters
		}
	}
}

// mergePass is one pass of mergeClose. A spatial hash of the kept clusters
// makes it linear in practice.
func mergePass(clusters []MapCluster, cell float64) []MapCluster {
	slices.SortStableFunc(clusters, func(a, b MapCluster) int { return cmp.Compare(b.Count, a.Count) })

	type kept struct {
		c    MapCluster
		x, y float64 // center in Web Mercator meters
	}
	var out []*kept
	grid := map[[2]int][]*kept{}
	key := func(x, y float64) [2]int { return [2]int{int(math.Floor(x / cell)), int(math.Floor(y / cell))} }

	for _, c := range clusters {
		x, y := mercator(c.Lon, c.Lat)
		var into *kept
		k := key(x, y)
	search:
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				for _, o := range grid[[2]int{k[0] + dx, k[1] + dy}] {
					if math.Hypot(o.x-x, o.y-y) < cell {
						into = o
						break search
					}
				}
			}
		}
		if into == nil {
			n := &kept{c: c, x: x, y: y}
			out = append(out, n)
			grid[k] = append(grid[k], n)
			continue
		}
		// Weighted center; the kept cluster stays in its grid slot, which is
		// still within a cell of where it is now.
		m := &into.c
		total := float64(m.Count + c.Count)
		m.Lon = (m.Lon*float64(m.Count) + c.Lon*float64(c.Count)) / total
		m.Lat = (m.Lat*float64(m.Count) + c.Lat*float64(c.Count)) / total
		m.Count += c.Count
		m.BBox = [4]float64{min(m.BBox[0], c.BBox[0]), min(m.BBox[1], c.BBox[1]), max(m.BBox[2], c.BBox[2]), max(m.BBox[3], c.BBox[3])}
		m.Route = nil
		into.x, into.y = mercator(m.Lon, m.Lat)
	}

	res := make([]MapCluster, len(out))
	for i, k := range out {
		res[i] = k.c
	}
	return res
}

// mercator projects WGS84 degrees to Web Mercator meters (EPSG:3857).
func mercator(lon, lat float64) (x, y float64) {
	const r = 6378137.0
	lat = max(-maxMercatorLat, min(maxMercatorLat, lat))
	return r * lon * math.Pi / 180, r * math.Log(math.Tan(math.Pi/4+lat*math.Pi/360))
}
