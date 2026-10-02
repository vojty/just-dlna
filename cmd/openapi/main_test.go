package main

import (
	"bytes"
	"os"
	"testing"
)

// TestUpToDate fails when the API changed without regenerating the contract.
func TestUpToDate(t *testing.T) {
	want, err := spec()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("openapi.json is out of date, run scripts/gen-api.sh")
	}
}
