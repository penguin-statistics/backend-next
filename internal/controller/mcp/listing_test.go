package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"unicode/utf8"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestListing checks server.json, the listing of the server in the MCP Registry, against the server: what it gets
// wrong is published for good (see listing.go). The rules of its schema are the registry's to check (mcp-publisher
// validate, in the workflow).
func TestListing(t *testing.T) {
	b, err := os.ReadFile("../../../server.json")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Schema      string `json:"$schema"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Version     string `json:"version"`
		WebsiteURL  string `json:"websiteUrl"`
		Repository  struct {
			URL    string `json:"url"`
			Source string `json:"source"`
			ID     string `json:"id"`
		} `json:"repository"`
		Icons   []sdkmcp.Icon `json:"icons"`
		Remotes []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"remotes"`
	}
	// a field this test does not know of fails it, so that every field is checked
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&listing); err != nil {
		t.Fatal(err)
	}
	if listing.Schema == "" {
		t.Error("no $schema, which the registry requires")
	}
	for _, f := range []struct{ field, got, want string }{
		{"name", listing.Name, registryName},
		{"title", listing.Title, serverTitle},
		{"description", listing.Description, serverDescription},
		{"version", listing.Version, listingVersion},
		{"websiteUrl", listing.WebsiteURL, manualURL},
		{"repository.url", listing.Repository.URL, repositoryURL},
		{"repository.source", listing.Repository.Source, "github"},
		{"repository.id", listing.Repository.ID, repositoryID},
	} {
		if f.got != f.want {
			t.Errorf("%s %q, want %q", f.field, f.got, f.want)
		}
	}
	// the registry refuses a longer title or description
	if utf8.RuneCountInString(serverTitle) > 100 || utf8.RuneCountInString(serverDescription) > 100 {
		t.Errorf("title %q or description %q longer than 100 characters", serverTitle, serverDescription)
	}
	// the icons of serverInfo in production (TestInitialize; site is the production origin)
	if !reflect.DeepEqual(listing.Icons, wantServerInfo.Icons) {
		t.Errorf("icons %+v, want %+v", listing.Icons, wantServerInfo.Icons)
	}
	// the stable URL only: the registry gives a remote URL to one name, and a preview host has no place in a listing
	// that is permanent
	if len(listing.Remotes) != 1 || listing.Remotes[0].Type != "streamable-http" || listing.Remotes[0].URL != publicURL {
		t.Errorf("remotes %+v, want one streamable-http at %s", listing.Remotes, publicURL)
	}
}
