package main

import (
	"fmt"
	"strings"
	"unicode"
)

// Official trail datasets whose licenses allow reuse. Each was checked
// against the publisher's terms; keep the attribution text as it is.
var (
	usCoverage = []BBox{
		{24, -125, 50, -66},      // contiguous states
		{51, -180, 72, -129},     // Alaska
		{18, -161, 23, -154},     // Hawaii
		{17.5, -68, 18.6, -64.5}, // Puerto Rico, Virgin Islands
	}

	// U.S. National Park Service public trails. Works of the U.S. federal
	// government are in the public domain (17 U.S.C. § 105); the service
	// adds only a disclaimer. Trails are stored as segments, joined by park
	// and trail name.
	npsTrails = ArcGISDataset{
		ID:       "nps",
		Layer:    "https://mapservices.nps.gov/arcgis/rest/services/NationalDatasets/NPS_Public_Trails/FeatureServer/0",
		Coverage: usCoverage,
		Where: "TRLNAME IS NOT NULL AND TRLNAME <> ' ' AND TRLSTATUS IN ('Existing', 'Exisiting')" +
			" AND TRLTYPE NOT IN ('Sidewalk', 'Water Trail', 'Snow Trail', 'Ferry Route', 'Steps')" +
			" AND TRLNAME NOT LIKE '%Sidewalk%'",
		OIDField:    "OBJECTID",
		KeyFields:   []string{"UNITCODE", "TRLNAME"},
		Fields:      []string{"UNITNAME", "TRLUSE"},
		Attribution: "Trail data: U.S. National Park Service, NPS Public Trails (public domain).",
		Describe: func(a attrs, t *Track) {
			t.Name = a.String("TRLNAME")
			t.Description = "Trail in " + a.String("UNITNAME") + "."
			if use := a.String("TRLUSE"); use != "" && use != "Unknown" {
				t.Description += " Use: " + use + "."
			}
			t.URL = "https://www.nps.gov/" + strings.ToLower(a.String("UNITCODE")) + "/"
		},
	}

	// USDA Forest Service National Forest System trails, published for
	// public use (CC BY 4.0 on data.gov). Segments of a trail share its
	// trail_cn.
	usfsTrails = ArcGISDataset{
		ID:          "usfs",
		Layer:       "https://apps.fs.usda.gov/arcx/rest/services/EDW/EDW_TrailNFSPublish_01/MapServer/0",
		Coverage:    usCoverage,
		Where:       "trail_type = 'TERRA' AND trail_name IS NOT NULL AND trail_name <> 'NA'",
		OIDField:    "objectid",
		KeyFields:   []string{"trail_cn"},
		Fields:      []string{"trail_name", "trail_no"},
		Attribution: "Trail data: USDA Forest Service, National Forest System Trails (CC BY 4.0).",
		Describe: func(a attrs, t *Track) {
			t.Name = titleCase(a.String("trail_name"))
			t.Description = "National Forest System trail"
			if no := a.String("trail_no"); no != "" {
				t.Description += " " + no
			}
			t.Description += "."
			t.URL = "https://data.fs.usda.gov/geodata/edw/datasets.php"
		},
	}

	// New Zealand Department of Conservation tracks: Crown copyright,
	// CC BY 4.0. One feature per track.
	docNZTracks = ArcGISDataset{
		ID:          "doc-nz",
		Layer:       "https://services1.arcgis.com/3JjYDyG3oajxU6HO/ArcGIS/rest/services/DOC_Tracks_Open_Data/FeatureServer/0",
		Coverage:    []BBox{{-47.5, 166, -34, 179}},
		Where:       "CharName2 = 'USER_STATUS' AND CharValue2 = 'OPEN' AND TechObjectName IS NOT NULL",
		OIDField:    "OBJECTID",
		KeyFields:   []string{"FlocID"},
		Fields:      []string{"TechObjectName", "CharValue3", "CharValue4", "CharValue7"},
		Attribution: "Contains data sourced from the Department of Conservation (DOC), Crown copyright, licensed under CC BY 4.0.",
		Describe: func(a attrs, t *Track) {
			t.Name = a.String("TechObjectName")
			var parts []string
			for _, f := range []string{"CharValue3", "CharValue4", "CharValue7"} { // category, walking time, track type
				if v := a.String(f); v != "" {
					parts = append(parts, v)
				}
			}
			t.Description = "Department of Conservation track"
			if len(parts) > 0 {
				t.Description += ": " + strings.Join(parts, ", ")
			}
			t.Description += "."
			t.URL = "https://www.doc.govt.nz/parks-and-recreation/things-to-do/walking-and-tramping/"
		},
	}

	// Parks Canada trails: Open Government Licence – Canada. Segments are
	// joined by place (BAID) and official name.
	parksCanadaTrails = ArcGISDataset{
		ID:          "parks-canada",
		Layer:       "https://services2.arcgis.com/wCOMu5IS7YdSyPNx/arcgis/rest/services/vw_Trails_Sentiers_APCA_V2_FGP/FeatureServer/0",
		Coverage:    []BBox{{41.6, -141.1, 83.2, -52.6}},
		Where:       "Official_Officiel = 1 AND Name_Official_e IS NOT NULL AND Name_Official_e NOT LIKE '% Road'",
		OIDField:    "OBJECTID",
		KeyFields:   []string{"BAID", "Name_Official_e"},
		Fields:      []string{"URL_e"},
		Attribution: "Contains information licensed under the Open Government Licence – Canada (Parks Canada).",
		Describe: func(a attrs, t *Track) {
			t.Name = a.String("Name_Official_e")
			t.Description = "Parks Canada trail."
			t.URL = a.String("URL_e")
			if t.URL == "" {
				t.URL = "https://parks.canada.ca/"
			}
		},
	}

	datasets = []ArcGISDataset{npsTrails, usfsTrails, docNZTracks, parksCanadaTrails}
)

// titleCase turns "BEAR CREEK #2" into "Bear Creek #2".
func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// sourceNames lists every source, for flags and errors.
func sourceNames() string {
	names := []string{"osm"}
	for _, d := range datasets {
		names = append(names, d.ID)
	}
	return strings.Join(names, ", ")
}

func datasetByID(id string) (ArcGISDataset, error) {
	for _, d := range datasets {
		if d.ID == id {
			return d, nil
		}
	}
	return ArcGISDataset{}, fmt.Errorf("unknown source %q; sources: %s", id, sourceNames())
}
