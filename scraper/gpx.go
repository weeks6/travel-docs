package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// GPX renders the track as a GPX 1.1 document: one track, one segment per
// piece. Metadata carries the name, description and source link; the
// attribution is added to the route description after import.
func (t Track) GPX() []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<gpx version="1.1" creator="route-scraper" xmlns="http://www.topografix.com/GPX/1/1">` + "\n")
	b.WriteString("  <metadata>\n")
	writeElem(&b, "    ", "name", t.Name)
	writeElem(&b, "    ", "desc", t.Description)
	fmt.Fprintf(&b, "    <link href=\"%s\"></link>\n", escape(t.URL))
	b.WriteString("  </metadata>\n  <trk>\n")
	writeElem(&b, "    ", "name", t.Name)
	for _, seg := range t.Segments {
		b.WriteString("    <trkseg>\n")
		for _, p := range seg {
			fmt.Fprintf(&b, "      <trkpt lat=\"%.7f\" lon=\"%.7f\"></trkpt>\n", p.Lat, p.Lon)
		}
		b.WriteString("    </trkseg>\n")
	}
	b.WriteString("  </trk>\n</gpx>\n")
	return b.Bytes()
}

func writeElem(b *bytes.Buffer, indent, name, text string) {
	fmt.Fprintf(b, "%s<%s>%s</%s>\n", indent, name, escape(text), name)
}

func escape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
