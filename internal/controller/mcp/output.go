package mcp

import (
	"errors"

	"exusiai.dev/backend-next/internal/service"
)

// What the results of the tools share.

// errCold is the error of a tool that needs data that is not loaded yet: the catalog for a few seconds after a start,
// the matrix of a server for up to a few minutes (see service.SEOData). The SDK sends its text as the result, an
// error (isError) that the model can act on; the metrics count it as cold. A structured result with the time to
// wait is not possible: a result with an output schema must match it, errors included.
var errCold = errors.New("Penguin Statistics is still loading its data (the server restarted recently). Retry in about 2 minutes.")

// catalog returns the catalog, or errCold before it is loaded.
func (t *tools) catalog() (*service.SEOCatalog, error) {
	if c := t.data.Catalog(); c != nil {
		return c, nil
	}
	return nil, errCold
}

// matrix returns the matrix of server, or errCold while it is not calculated.
func (t *tools) matrix(server string) (*service.SEOMatrix, error) {
	if m := t.data.Matrix(server); m != nil {
		return m, nil
	}
	return nil, errCold
}
