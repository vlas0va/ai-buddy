// Пакет contracts встраивает машиночитаемые контракты, чтобы сервисы загружали ровно те схемы инструментов, которые
// обещают.
package contracts

import "embed"

// Schemas содержит schemas/<server>.json вида {"server": ..., "tools": [{name, description, roles, write, inputSchema,
// outputSchema}]}.
//
//go:embed schemas/*.json
var Schemas embed.FS
