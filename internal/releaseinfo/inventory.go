// Package releaseinfo embeds the release inventory in installed binaries.
package releaseinfo

import _ "embed"

// Inventory is a copy of release/inventory.json. The release verifier rejects
// a build when the two copies differ.
//
//go:embed inventory.json
var Inventory []byte
