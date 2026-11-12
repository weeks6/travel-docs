package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ArcGISDataset describes a trail layer published through an ArcGIS REST
// service (FeatureServer or MapServer), as many park and forest agencies
// do. Such layers usually store a trail as several line segments; features
// with equal KeyFields are joined into one route.
type ArcGISDataset struct {
	ID          string   // source name and SourceID prefix, e.g. "nps"
	Layer       string   // layer URL, …/FeatureServer/0 or …/MapServer/0
	Coverage    []BBox   // where the dataset has data; other regions are skipped
	Where       string   // SQL filter for features that are usable trails
	OIDField    string   // object ID field, for stable paging
	KeyFields   []string // features with equal values form one route
	Fields      []string // other attributes Describe reads
	Attribution string   // license notice the publisher requires
	// Describe sets the track's name, description and URL from the first
	// feature's attributes.
	Describe func(a attrs, t *Track)
}

// attrs are a feature's attributes.
type attrs map[string]any

// String returns an attribute as trimmed text ("" when null).
func (a attrs) String(key string) string {
	switch v := a[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// ArcGISSource reads one ArcGIS dataset, region by region.
type ArcGISSource struct {
	Dataset   ArcGISDataset
	UserAgent string
	HTTP      *http.Client
	// Backoff is the first wait after a server error (default 5 s).
	Backoff time.Duration
}

func (s *ArcGISSource) Name() string { return s.Dataset.ID }

const (
	arcgisPage     = 1000 // features per request; layers allow up to 2000
	arcgisKeyBatch = 40   // routes per geometry request
	joinTolM       = 15   // segment ends this close are joined
)

// Tracks returns the routes that cross the region, complete: it first
// finds which routes have a segment in the region (attributes only), then
// fetches every segment of those routes, inside the region or not.
func (s *ArcGISSource) Tracks(ctx context.Context, r Region) ([]Track, error) {
	d := s.Dataset
	if !d.covers(r.BBox) {
		return nil, nil
	}
	feats, err := s.query(ctx, url.Values{
		"where":          {d.Where},
		"geometry":       {fmt.Sprintf("%f,%f,%f,%f", r.West, r.South, r.East, r.North)},
		"geometryType":   {"esriGeometryEnvelope"},
		"inSR":           {"4326"},
		"spatialRel":     {"esriSpatialRelIntersects"},
		"outFields":      {strings.Join(d.KeyFields, ",")},
		"returnGeometry": {"false"},
	})
	if err != nil {
		return nil, err
	}
	keys := map[string]attrs{}
	for _, f := range feats {
		keys[d.key(f.Attributes)] = f.Attributes
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var out []Track
	for start := 0; start < len(ordered); start += arcgisKeyBatch {
		batch := ordered[start:min(start+arcgisKeyBatch, len(ordered))]
		var match []string
		for _, k := range batch {
			match = append(match, d.matchKey(keys[k]))
		}
		feats, err := s.query(ctx, url.Values{
			"where":              {"(" + d.Where + ") AND (" + strings.Join(match, " OR ") + ")"},
			"outFields":          {strings.Join(append(append([]string{}, d.KeyFields...), d.Fields...), ",")},
			"returnGeometry":     {"true"},
			"outSR":              {"4326"},
			"geometryPrecision":  {"6"},
			"maxAllowableOffset": {"0.00001"}, // ~1 m: drops redundant vertices
		})
		if err != nil {
			return out, err
		}
		groups := map[string][]arcgisFeature{}
		for _, f := range feats {
			k := d.key(f.Attributes)
			groups[k] = append(groups[k], f)
		}
		for _, k := range batch {
			if fs := groups[k]; len(fs) > 0 {
				out = append(out, d.track(k, fs))
			}
		}
	}
	return out, nil
}

func (d ArcGISDataset) covers(b BBox) bool {
	for _, c := range d.Coverage {
		if c.Intersects(b) {
			return true
		}
	}
	return false
}

// key identifies a route by its key field values.
func (d ArcGISDataset) key(a attrs) string {
	parts := make([]string, len(d.KeyFields))
	for i, f := range d.KeyFields {
		parts[i] = a.String(f)
	}
	return strings.Join(parts, "|")
}

// matchKey is a SQL condition matching the features of one route.
func (d ArcGISDataset) matchKey(a attrs) string {
	conds := make([]string, len(d.KeyFields))
	for i, f := range d.KeyFields {
		switch v := a[f].(type) {
		case float64:
			conds[i] = fmt.Sprintf("%s = %s", f, strconv.FormatFloat(v, 'f', -1, 64))
		case nil:
			conds[i] = f + " IS NULL"
		default:
			conds[i] = fmt.Sprintf("%s = '%s'", f, strings.ReplaceAll(fmt.Sprint(v), "'", "''"))
		}
	}
	return "(" + strings.Join(conds, " AND ") + ")"
}

func (d ArcGISDataset) track(key string, fs []arcgisFeature) Track {
	var pieces [][]Point
	for _, f := range fs {
		if f.Geometry == nil {
			continue
		}
		for _, path := range f.Geometry.Paths {
			p := make([]Point, 0, len(path))
			for _, xy := range path {
				if len(xy) >= 2 {
					p = append(p, Point{Lat: xy[1], Lon: xy[0]})
				}
			}
			pieces = append(pieces, p)
		}
	}
	t := Track{
		SourceID:    d.ID + ":" + key,
		Attribution: d.Attribution,
		Segments:    joinSegments(pieces, joinTolM),
	}
	d.Describe(fs[0].Attributes, &t)
	return t
}

type arcgisFeature struct {
	Attributes attrs `json:"attributes"`
	Geometry   *struct {
		Paths [][][]float64 `json:"paths"`
	} `json:"geometry"`
}

type arcgisResponse struct {
	Features              []arcgisFeature `json:"features"`
	ExceededTransferLimit bool            `json:"exceededTransferLimit"`
	Error                 *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// query runs a layer query, following pages until the server has no more.
func (s *ArcGISSource) query(ctx context.Context, params url.Values) ([]arcgisFeature, error) {
	var all []arcgisFeature
	params.Set("f", "json")
	params.Set("orderByFields", s.Dataset.OIDField)
	params.Set("resultRecordCount", strconv.Itoa(arcgisPage))
	for {
		// Servers may return fewer than asked (their own page limit), so the
		// next page starts after what came back.
		params.Set("resultOffset", strconv.Itoa(len(all)))
		res, err := s.post(ctx, params)
		if err != nil {
			return all, err
		}
		all = append(all, res.Features...)
		if !res.ExceededTransferLimit || len(res.Features) == 0 {
			return all, nil
		}
	}
}

// post sends one query, retrying with backoff while the server is busy.
func (s *ArcGISSource) post(ctx context.Context, params url.Values) (*arcgisResponse, error) {
	backoff := s.Backoff
	if backoff == 0 {
		backoff = 5 * time.Second
	}
	for attempt := 1; ; attempt++ {
		res, retry, err := s.postOnce(ctx, params)
		if err == nil {
			return res, nil
		}
		if !retry || attempt == 4 {
			return nil, fmt.Errorf("%s: %w", s.Dataset.ID, err)
		}
		if err := sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
	}
}

func (s *ArcGISSource) postOnce(ctx context.Context, params url.Values) (res *arcgisResponse, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", s.Dataset.Layer+"/query", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("server busy (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, body)
	}
	res = &arcgisResponse{}
	if err := json.Unmarshal(body, res); err != nil {
		return nil, true, fmt.Errorf("unexpected response: %.120s", body)
	}
	// ArcGIS reports errors in a 200 response.
	if res.Error != nil {
		return nil, res.Error.Code >= 500, fmt.Errorf("error %d: %s", res.Error.Code, res.Error.Message)
	}
	return res, false, nil
}
