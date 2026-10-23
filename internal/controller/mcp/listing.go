package mcp

// The listing of the server in the official MCP Registry (https://registry.modelcontextprotocol.io) is server.json, at
// the root of the repository, which .github/workflows/mcp-registry.yml publishes; directories, clients and the
// aggregators copying the registry find the server there. A listing is permanent: the registry takes a version once
// and keeps it forever, so a wrong one is only fixed by publishing the next. TestListing keeps server.json equal to
// these constants and to serverInfo.
const (
	// registryName is the name of the server in the registry: the namespace of penguin-stats.io, which its proof
	// (/.well-known/mcp-registry-auth, served by frontend-v2) lets us publish under, then serverName. It cannot
	// change: another name would be another listing, and the registry gives a remote URL to one name only.
	registryName = "io.penguin-stats/penguin-stats"
	// listingVersion is the version of the listing, not of the backend (released several times a day, which would
	// pile up versions saying the same): bump it with every change of server.json, as the registry refuses a version
	// it already has
	listingVersion = "1.0.0"
	// publicURL is where clients reach the server, the remote of the listing
	publicURL = "https://mcp.penguin-stats.io/mcp"
	// manualURL is the page telling people how to add the server to their clients, the website of the listing
	manualURL = "https://mcp.penguin-stats.io/"
	// repositoryURL and repositoryID are this repository on GitHub; the ID survives a rename or a transfer
	repositoryURL = "https://github.com/penguin-statistics/backend-next"
	repositoryID  = "431197141"
)
