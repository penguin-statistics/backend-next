package mcp

import (
	"encoding/json"
	"net/url"

	"github.com/gofiber/fiber/v2"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"exusiai.dev/backend-next/internal/pkg/middlewares"
)

// The MCP Server Card (SEP-2127; its format is that of https://github.com/modelcontextprotocol/ext-server-card at
// 526201bb, the snapshot the SEP accepted) tells clients and directories what the server is and where to reach it,
// before they connect. It is served at the URL the specification reserves for it, the endpoint followed by
// /server-card, and an AI Catalog entry or a Link header points to it. It says what serverInfo and server.json say:
// the specification asks a card not to contradict serverInfo, and checkers compare it with the registry listing.

const (
	// serverCardSchema is the $schema every card must have (the pattern of the schema allows no other)
	serverCardSchema = "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json"
	mimeServerCard   = "application/mcp-server-card+json"
	// the card changes with a release only; clients and CDNs keep it for an hour, as the specification suggests
	serverCardCacheControl = "public, max-age=3600"
)

type serverCard struct {
	Schema      string         `json:"$schema"`
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Title       string         `json:"title"`
	WebsiteURL  string         `json:"websiteUrl"`
	Repository  cardRepository `json:"repository"`
	Icons       []sdkmcp.Icon  `json:"icons"`
	Remotes     []cardRemote   `json:"remotes"`
}

type cardRepository struct {
	URL    string `json:"url"`
	Source string `json:"source"`
	ID     string `json:"id"`
}

type cardRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	// SupportedProtocolVersions are all the SDK's: the server is stateless, so it serves each of them
	SupportedProtocolVersions []string `json:"supportedProtocolVersions"`
}

// newServerCard returns the card of the server whose public URL is endpoint (MCPPublicURL), on the site of origin
// site (SEOSiteURL), which has the icons. Its website is the guide to adding the server to clients, at the root of the
// host of endpoint, as the website of the listing is.
func newServerCard(endpoint *url.URL, site string) serverCard {
	return serverCard{
		Schema:      serverCardSchema,
		Name:        registryName,
		Version:     listingVersion,
		Description: serverDescription,
		Title:       serverTitle,
		WebsiteURL:  guideURL(endpoint),
		Repository:  cardRepository{URL: repositoryURL, Source: "github", ID: repositoryID},
		Icons:       icons(site),
		Remotes: []cardRemote{{
			Type:                      "streamable-http",
			URL:                       endpoint.String(),
			SupportedProtocolVersions: sdkmcp.SupportedProtocolVersions(),
		}},
	}
}

// guideURL is the URL of the guide of the server whose public URL is endpoint: the root of its host
func guideURL(endpoint *url.URL) string {
	return endpoint.Scheme + "://" + endpoint.Host + "/"
}

// mountServerCard serves card at middlewares.MCPServerCardPath, to GET and HEAD whatever their Accept (a client that
// asks for application/json gets the card all the same). It is outside the rate and concurrency limits of /mcp: a
// few hundred bytes made once, which clients and CDNs keep.
func mountServerCard(r fiber.Router, card serverCard) error {
	b, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		return err
	}
	r.Get(middlewares.MCPServerCardPath, newStatic(mimeServerCard, serverCardCacheControl, append(b, '\n')).send)
	return nil
}
