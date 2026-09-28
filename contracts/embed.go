package contracts

import (
	"embed"
	"io/fs"
)

//go:embed protocol http admin-ui
var contractAssets embed.FS

// Files exposes only generic protocol and shared HTTP contracts.
func Files() fs.FS {
	return contractAssets
}
