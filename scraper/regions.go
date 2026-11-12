package main

import (
	"fmt"
	"sort"
	"strings"
)

// Region is a named area to scan. Regions are small (well under a degree or
// so on each side) because Overpass queries over large, route-dense areas
// time out, and national datasets are fetched per region too.
type Region struct {
	Name string
	BBox
}

// regionGroups are the built-in regions: well-known hiking and cycling areas
// on every continent. Use them by name ("yosemite"), by group ("asia"), or
// all of them ("world").
var regionGroups = map[string][]Region{
	"europe": {
		{"lauterbrunnen", BBox{46.45, 7.75, 46.7, 8.1}},
		{"chamonix", BBox{45.8, 6.75, 46.05, 7.1}},
		{"dolomites", BBox{46.4, 11.6, 46.7, 12.3}},
		{"ordesa", BBox{42.55, -0.2, 42.8, 0.2}},
		{"lake-district", BBox{54.35, -3.3, 54.65, -2.8}},
		{"tatras", BBox{49.1, 19.7, 49.3, 20.3}},
		{"picos-de-europa", BBox{43.1, -5.1, 43.3, -4.6}},
		{"lofoten", BBox{67.9, 13.0, 68.4, 14.6}},
	},
	"north-america": {
		{"yosemite", BBox{37.5, -119.8, 38.0, -119.2}},
		{"grand-canyon", BBox{35.9, -112.4, 36.3, -111.8}},
		{"zion", BBox{37.1, -113.2, 37.5, -112.85}},
		{"rocky-mountain", BBox{40.15, -105.9, 40.55, -105.5}},
		{"glacier", BBox{48.5, -114.2, 49.0, -113.4}},
		{"olympic", BBox{47.6, -124.0, 48.1, -123.2}},
		{"great-smoky", BBox{35.45, -83.9, 35.8, -83.1}},
		{"white-mountains", BBox{44.0, -71.6, 44.4, -71.1}},
		{"banff", BBox{51.0, -116.3, 51.5, -115.4}},
		{"whistler", BBox{49.95, -123.2, 50.2, -122.8}},
		{"monteverde", BBox{10.2, -84.9, 10.4, -84.7}},
	},
	"south-america": {
		{"torres-del-paine", BBox{-51.2, -73.3, -50.8, -72.8}},
		{"el-chalten", BBox{-49.5, -73.1, -49.2, -72.8}},
		{"cordillera-blanca", BBox{-9.6, -77.7, -8.8, -77.3}},
		{"sacred-valley", BBox{-13.6, -72.7, -13.1, -71.9}},
		{"serra-dos-orgaos", BBox{-22.6, -43.2, -22.35, -42.9}},
	},
	"africa": {
		{"table-mountain", BBox{-34.2, 18.3, -33.9, 18.5}},
		{"drakensberg", BBox{-29.4, 29.2, -28.6, 29.5}},
		{"kilimanjaro", BBox{-3.3, 37.1, -2.9, 37.6}},
		{"toubkal", BBox{31.0, -8.0, 31.2, -7.8}},
		{"simien", BBox{13.1, 37.9, 13.4, 38.4}},
	},
	"asia": {
		{"annapurna", BBox{28.2, 83.6, 28.8, 84.3}},
		{"khumbu", BBox{27.7, 86.5, 28.1, 86.9}},
		{"kamikochi", BBox{36.1, 137.5, 36.5, 137.8}},
		{"hakone-fuji", BBox{35.15, 138.6, 35.45, 139.1}},
		{"taroko", BBox{24.1, 121.3, 24.3, 121.7}},
		{"seoraksan", BBox{38.05, 128.3, 38.25, 128.55}},
		{"almaty", BBox{43.0, 76.8, 43.2, 77.2}},
		{"kazbegi", BBox{42.55, 44.4, 42.8, 44.7}},
		{"cappadocia", BBox{38.55, 34.75, 38.75, 34.95}},
	},
	"oceania": {
		{"blue-mountains", BBox{-33.9, 150.2, -33.5, 150.5}},
		{"cradle-mountain", BBox{-41.9, 145.8, -41.6, 146.1}},
		{"fiordland", BBox{-45.1, 167.6, -44.6, 168.2}},
		{"tongariro", BBox{-39.35, 175.5, -39.1, 175.75}},
		{"abel-tasman", BBox{-41.0, 172.85, -40.75, 173.05}},
		{"kauai", BBox{22.0, -159.8, 22.25, -159.3}},
	},
}

// groupOrder interleaves continents in "world", so every run covers more
// than one part of the world.
var groupOrder = []string{"europe", "north-america", "asia", "south-america", "oceania", "africa"}

// worldRegions takes one region from each continent in turn.
func worldRegions() []Region {
	var out []Region
	for i := 0; ; i++ {
		added := false
		for _, g := range groupOrder {
			if i < len(regionGroups[g]) {
				out = append(out, regionGroups[g][i])
				added = true
			}
		}
		if !added {
			return out
		}
	}
}

// ParseRegions reads a ';'-separated list of region names, group names
// ("world", "asia", …) and bounding boxes ("south,west,north,east").
func ParseRegions(spec string) ([]Region, error) {
	var out []Region
	seen := map[string]bool{}
	add := func(r Region) {
		if !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r)
		}
	}
	for _, item := range strings.Split(spec, ";") {
		item = strings.TrimSpace(item)
		switch {
		case item == "":
		case item == "world":
			for _, r := range worldRegions() {
				add(r)
			}
		case regionGroups[item] != nil:
			for _, r := range regionGroups[item] {
				add(r)
			}
		case strings.ContainsAny(item, "0123456789"):
			b, err := ParseBBox(item)
			if err != nil {
				return nil, err
			}
			add(Region{Name: item, BBox: b})
		default:
			r, ok := regionByName(item)
			if !ok {
				return nil, fmt.Errorf("unknown region %q; use a bbox (south,west,north,east) or one of: world, %s", item, regionNames())
			}
			add(r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no regions to scan")
	}
	return out, nil
}

func regionByName(name string) (Region, bool) {
	for _, rs := range regionGroups {
		for _, r := range rs {
			if r.Name == name {
				return r, true
			}
		}
	}
	return Region{}, false
}

// regionNames lists the groups, then every region, for help and errors.
func regionNames() string {
	var regions []string
	for _, rs := range regionGroups {
		for _, r := range rs {
			regions = append(regions, r.Name)
		}
	}
	sort.Strings(regions)
	return strings.Join(groupOrder, ", ") + ", " + strings.Join(regions, ", ")
}

// Intersects reports whether two boxes overlap.
func (b BBox) Intersects(o BBox) bool {
	return b.South < o.North && o.South < b.North && b.West < o.East && o.West < b.East
}
