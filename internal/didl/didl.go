// Package didl defines the DIDL-Lite objects returned by ContentDirectory
// Browse. dms wraps them in the <DIDL-Lite> element with the dc, upnp and dlna
// namespaces declared; anything else must declare its namespace inline.
package didl

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

const (
	ClassFolder = "object.container.storageFolder"
	ClassVideo  = "object.item.videoItem"
)

// Container is a browsable folder.
type Container struct {
	XMLName    xml.Name `xml:"container"`
	ID         string   `xml:"id,attr"`
	ParentID   string   `xml:"parentID,attr"`
	Restricted int      `xml:"restricted,attr"`
	Searchable int      `xml:"searchable,attr"`
	ChildCount int      `xml:"childCount,attr"`
	Title      string   `xml:"dc:title"`
	Class      string   `xml:"upnp:class"`
}

// Item is a playable object.
type Item struct {
	XMLName    xml.Name `xml:"item"`
	ID         string   `xml:"id,attr"`
	ParentID   string   `xml:"parentID,attr"`
	Restricted int      `xml:"restricted,attr"`
	Title      string   `xml:"dc:title"`
	Class      string   `xml:"upnp:class"`
	Date       string   `xml:"dc:date,omitempty"`
	Res        []Res    `xml:"res"`
	// Extra holds raw XML appended inside the item (client-specific elements).
	Extra string `xml:",innerxml"`
}

// Res is a resource (URL) of an item.
type Res struct {
	ProtocolInfo string `xml:"protocolInfo,attr"`
	Size         int64  `xml:"size,attr,omitempty"`
	Duration     string `xml:"duration,attr,omitempty"`
	Bitrate      uint   `xml:"bitrate,attr,omitempty"`
	Resolution   string `xml:"resolution,attr,omitempty"`
	URL          string `xml:",chardata"`
}

// Duration formats d as H:MM:SS.mmm, as used by the res@duration attribute.
func Duration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	ms := d.Milliseconds()
	return fmt.Sprintf("%d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// Date formats t for dc:date.
func Date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// Escape returns s escaped for use as XML character data or attribute value.
func Escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
