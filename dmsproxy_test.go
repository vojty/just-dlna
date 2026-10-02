package main

import "testing"

func TestDMSAllowed(t *testing.T) {
	for p, want := range map[string]bool{
		"/rootDesc.xml":              true,
		"/ctl":                       true,
		"/evt/ContentDirectory":      true,
		"/scpd/ContentDirectory.xml": true,
		"/deviceIcon/0":              true,
		"/":                          false,
		"/subtitle":                  false,
		"/icon":                      false,
		"/res":                       false,
		"/debug/pprof/":              false,
		"/scpd/../subtitle":          false,
	} {
		if got := dmsAllowed(p); got != want {
			t.Errorf("dmsAllowed(%q) = %v, want %v", p, got, want)
		}
	}
}
