package pluginprotocol

import (
	"embed"
	"io/fs"
)

//go:embed contracts
var contractAssets embed.FS

// ContractFiles exposes the protocol module's versioned declarative contracts.
// Paths are relative to this module's root, for example
// "contracts/forms-db/v1/admin-surface.json".
func ContractFiles() fs.FS {
	return contractAssets
}
