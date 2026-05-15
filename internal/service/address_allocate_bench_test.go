package service

// Wave 3 (KAC-94): pickRandomIPv4 / isUniqueViolation / usableIPv4Sweep
// переехали в `internal/apps/kacho/api/address/` вместе с AddressService.
// Эквивалентные бенчмарки следует добавить там после Wave 3 (см.
// `internal/apps/kacho/api/address/create.go::pickRandomIPv4`).
// Бенчмарк usableIPv4Count остаётся валиден через address_pool_service.go —
// но его уже здесь не выполняем, чтобы не плодить дубли.
