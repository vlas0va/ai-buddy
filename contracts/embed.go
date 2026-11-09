// Package contracts embeds the machine-readable contracts so services load the exact tool schemas they promise.
package contracts

import "embed"

// Schemas holds schemas/<server>.json with {"server": ..., "tools": [{name, description, roles, write, inputSchema, outputSchema}]}.
//
//go:embed schemas/*.json
var Schemas embed.FS
