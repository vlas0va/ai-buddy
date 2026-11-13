//go:build !demo

// Пакет buildflags хранит feature flags времени компиляции (R55). Собирайте с `-tags demo`, чтобы включить эндпоинты
// только для демо.
package buildflags

// Demo включает эндпоинты только для демо, например POST /api/cron/run и PUT /api/flags/{key}.
const Demo = false
