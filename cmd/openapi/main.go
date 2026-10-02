// Command openapi prints the OpenAPI document of the admin API, from which
// the UI's API client is generated (see scripts/gen-api.sh).
package main

import (
	"encoding/json"
	"log"
	"os"

	"just-dlna/internal/admin"
)

func main() {
	b, err := spec()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stdout.Write(b); err != nil {
		log.Fatal(err)
	}
}

func spec() ([]byte, error) {
	b, err := json.MarshalIndent(admin.OpenAPI(), "", "  ")
	return append(b, '\n'), err
}
