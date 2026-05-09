# 09 — Go Skills Applied (Refactor Log)

Trail-документ применения Go-скилов к kacho-vpc + связанным репо.
Каждая группа = отдельный commit в feature ветке. Помечается ✅/⚠️/⏭️.

| Skill | Status | Commit | Findings |
|---|:---:|---|---|
| golang-code-style | ✅ | group-1 | gofmt + layout pass |
| golang-naming | ✅ | group-1 | package/struct/interface naming |
| golang-modernize | ✅ | group-1 | go-modernize linter pass |
| golang-lint | ✅ | group-1 | golangci-lint config + fix |
| golang-continuous-integration | ⏳ | TBD | GitHub Actions workflow |
| golang-error-handling | ⏳ | TBD | wrap discipline, sentinels |
| golang-context | ⏳ | TBD | ctx propagation audit |
| golang-safety | ⏳ | TBD | nil-checks, defer in loops |
| golang-concurrency | ⏳ | TBD | goroutine ownership, leaks |
| golang-data-structures | ⏳ | TBD | slices, maps capacity |
| golang-design-patterns | ⏳ | TBD | functional options, lifecycle |
| golang-structs-interfaces | ⏳ | TBD | composition review |
| golang-dependency-injection | ⏳ | TBD | manual constructor injection review |
| golang-database | ⏳ | TBD | tx scoping, prepared stmts |
| golang-performance | ⏳ | TBD | hot-path (allocator, cascade) |
| golang-benchmark | ⏳ | TBD | benchmarks setup |
| golang-grpc | ⏳ | TBD | server setup, interceptors |
| golang-cli | ⏳ | TBD | kachoctl-ipam structure |
| golang-observability | ⏳ | TBD | slog discipline, metrics |
| golang-security | ⏳ | TBD | TLS, secrets, audit |
| golang-testing | ⏳ | TBD | coverage, table-driven |
| golang-stretchr-testify | ⏳ | TBD | assertion ergonomics |
| golang-troubleshooting | ⏳ | TBD | pprof setup |
| golang-project-layout | ⏳ | TBD | layout review |
| golang-documentation | ✅ | already done | `docs/architecture/` 8 files |

## Skipped (с обоснованием)

| Skill | Причина |
|---|---|
| golang-popular-libraries | Не refactor — discovery |
| golang-stay-updated | Не refactor — community resources |
| golang-dependency-management | Покрывается golang-lint+ci (Dependabot) |
| golang-graphql | У нас gRPC, GraphQL не требуется |
| golang-google-wire | Внести DI framework — big change, не сейчас |
| golang-uber-dig | То же |
| golang-uber-fx | То же |
| golang-samber-do | То же |
| golang-samber-lo | Functional helpers — введение new lib |
| golang-samber-mo | Monads — введение new lib |
| golang-samber-ro | Reactive — overkill |
| golang-samber-hot | In-mem cache — пока не нужен (нет hot read paths) |
| golang-samber-oops | Отдельная error lib — пока обходимся stdlib |
| golang-samber-slog | Sampling/multi-handler — пока обходимся stdlib slog |
| golang-spf13-viper | envconfig покрывает наши нужды |
| golang-spf13-cobra | flag достаточно для kachoctl-ipam |
| golang-swagger | grpc-gateway генерит OpenAPI; ручной swagger не нужен |
