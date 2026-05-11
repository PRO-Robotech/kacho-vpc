package ports

import "errors"

// Sentinel-ошибки слоя service/repo. Живут здесь (в leaf-пакете ports), а не в
// `internal/service`, чтобы общий test-helper `internal/ports/portmock` мог их
// возвращать без зависимости от `internal/service` (иначе — import-cycle с
// white-box service-тестами). `internal/service` ре-экспортирует их через
// type-alias'ы (`var ErrNotFound = ports.ErrNotFound` — тот же error-value, так
// что `errors.Is` работает прозрачно).

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = errors.New("already exists")

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = errors.New("invalid argument")

// ErrFailedPrecondition возвращается, когда операция отклонена из-за состояния
// ресурса (например, попытка удалить Network с зависимыми Subnets — нарушение FK
// в Postgres SQLSTATE 23503). Маппится в gRPC FailedPrecondition (как у YC:
// "Network is not empty").
var ErrFailedPrecondition = errors.New("failed precondition")

// ErrInternal — generic-ошибка для неклассифицированных DB-проблем. Маппится
// на gRPC Internal с фиксированным сообщением, чтобы не leak'ать pgx-текст.
var ErrInternal = errors.New("internal database error")

// ErrPoolNotResolved — ни один шаг IPAM cascade не дал результат.
// Маппится в FailedPrecondition. Тестируется через errors.Is.
var ErrPoolNotResolved = errors.New("no address pool resolved")

// ErrInvalidIPv4 — попытка allocate IP из не-IPv4 CIDR.
// Маппится в InvalidArgument.
var ErrInvalidIPv4 = errors.New("not ipv4")
