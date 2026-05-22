// Package main — отдельный binary `kacho-migrator` (skill evgeniy §9 K.1–K.3,
// AP-9). До KAC-96 миграции жили subcommand'ом в `cmd/vpc/main.go`
// (`kacho-vpc migrate up|down|status`) — это запрещённый «smashed cmd-binary».
//
// Теперь `cmd/vpc/main.go` обслуживает только `serve`, а CLI миграций — здесь.
// API совпадает с `goose`-flavour:
//
//	kacho-migrator up [--target <version>]
//	kacho-migrator down [--target <version>]
//	kacho-migrator status
//	kacho-migrator create <name> [--dir <path>]
//
// Флаги верхнего уровня:
//
//	--dialect oneof<postgres|cockroach>   (default postgres; K.3 — multi-dialect-ready)
//	--dsn     <connection-string>         (или ENV KACHO_MIGRATOR_DSN)
//
// Помимо ENV KACHO_MIGRATOR_DSN, для удобства dev-стенда (тот же набор переменных,
// что и у kacho-vpc) поддерживается fallback: если --dsn пуст и
// KACHO_MIGRATOR_DSN пуст, читаем `config.Load()` (envconfig) и берём
// `cfg.MigrateDSN()`. Это позволяет одному helm-values задавать БД-параметры
// для обоих binary, не дублируя DSN. Пользователь может явно передать --dsn,
// и vpc-config будет проигнорирован.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // регистрирует "pgx" driver для sql.Open
	"github.com/spf13/cobra"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/config"
	"github.com/PRO-Robotech/kacho-vpc/internal/apps/migrator"
	"github.com/PRO-Robotech/kacho-vpc/internal/migrations"
)

const (
	// defaultDialect — SQL-диалект по умолчанию, если флаг --dialect не задан.
	defaultDialect = "postgres"
	// defaultMigrationsDir — каталог с миграциями по умолчанию для чтения (embed FS root).
	defaultMigrationsDir = "."
	// defaultPhysDir — куда `create` пишет новые миграции по умолчанию.
	// На внешнем диске (relative cwd), не в embed FS — embed read-only.
	defaultPhysDir = "internal/migrations"
	// envDSN — имя env-переменной, из которой берётся DSN при отсутствии флага --dsn.
	envDSN = "KACHO_MIGRATOR_DSN"
)

// rootOptions — shared параметры всех subcommand'ов, накапливаются persistent-флагами.
type rootOptions struct {
	dialect string
	dsn     string
}

func main() {
	if err := newRootCmd(migrations.FS).Execute(); err != nil {
		// cobra сам печатает текст ошибки + usage; нам остаётся только exit-code.
		// Не пишем еще раз — было бы дублирование.
		os.Exit(1)
	}
}

// newRootCmd собирает дерево команд. Вынесено в отдельный конструктор,
// чтобы main_test.go мог инстанцировать и парсить args без os.Exit.
// migrationsFS принимается параметром: в production — `internal/migrations.FS`,
// в тестах — пустая `fstest.MapFS{}` (нам важно проверить парсинг args).
func newRootCmd(migrationsFS fs.FS) *cobra.Command {
	opts := &rootOptions{}

	root := &cobra.Command{
		Use:   "kacho-migrator",
		Short: "Database migrations runner for kacho-vpc (KAC-96)",
		Long: "kacho-migrator — отдельный CLI для управления миграциями БД сервиса kacho-vpc.\n" +
			"До KAC-96 был subcommand'ом `kacho-vpc migrate`; вынесен в отдельный\n" +
			"binary по правилу skill evgeniy §9 K.1 (отдельная точка сборки на use-case).",
		SilenceUsage: true, // не показывать usage на runtime-ошибках (только на parse-ошибках)
	}
	root.PersistentFlags().StringVar(&opts.dialect, "dialect", defaultDialect,
		"SQL dialect (postgres|cockroach)")
	root.PersistentFlags().StringVar(&opts.dsn, "dsn", "",
		"database DSN; if empty — read ENV "+envDSN+", then fall back to kacho-vpc config (envconfig)")

	root.AddCommand(
		newUpCmd(opts, migrationsFS),
		newDownCmd(opts, migrationsFS),
		newStatusCmd(opts, migrationsFS),
		newCreateCmd(opts, migrationsFS),
	)
	return root
}

// newUpCmd собирает subcommand `up` — применяет миграции до последней (или до --target).
func newUpCmd(opts *rootOptions, migrationsFS fs.FS) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply migrations up to latest (or --target version)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := buildRunner(opts, migrationsFS)
			if err != nil {
				return err
			}
			return r.Up(target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "stop at this version (inclusive); default — latest")
	return cmd
}

// newDownCmd собирает subcommand `down` — откатывает последнюю миграцию (или до --target).
func newDownCmd(opts *rootOptions, migrationsFS fs.FS) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Rollback the most recent migration (or down to --target)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := buildRunner(opts, migrationsFS)
			if err != nil {
				return err
			}
			return r.Down(target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "rollback down to this version (inclusive); default — one step back")
	return cmd
}

// newStatusCmd собирает cobra-команду `status` (показ applied/pending миграций).
func newStatusCmd(opts *rootOptions, migrationsFS fs.FS) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show migration status (applied / pending)",
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := buildRunner(opts, migrationsFS)
			if err != nil {
				return err
			}
			return r.Status(cmd.OutOrStdout())
		},
	}
}

// newCreateCmd собирает cobra-команду `create` (создание нового пустого SQL-файла миграции).
func newCreateCmd(opts *rootOptions, migrationsFS fs.FS) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new empty SQL migration file (on disk, not in embed FS)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := buildRunner(opts, migrationsFS)
			if err != nil {
				return err
			}
			return r.Create(dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", defaultPhysDir,
		"physical directory to place the new .sql file (cannot be embed FS)")
	return cmd
}

// buildRunner собирает migrator.Runner из persistent-флагов + ENV + config-fallback.
//
// Источник DSN — приоритет: --dsn flag > ENV KACHO_MIGRATOR_DSN > envconfig
// (config.Load → cfg.MigrateDSN). Так одно helm-values покрывает оба binary,
// и можно явно перекрыть `--dsn` для cross-DB-инструментов и ad-hoc запусков.
func buildRunner(opts *rootOptions, migrationsFS fs.FS) (*migrator.Runner, error) {
	dialect, err := migrator.ResolveDialect(opts.dialect)
	if err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(opts.dsn)
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv(envDSN))
	}
	if dsn == "" {
		// Fallback к vpc-envconfig: тот же DB_HOST/PORT/USER/PASSWORD/NAME/SSLMODE.
		// Если KACHO_VPC_DB_PASSWORD не выставлен — config.Load() вернёт ошибку
		// «required: KACHO_VPC_DB_PASSWORD», что и есть желаемое UX (явное «set
		// DSN или vpc-creds», а не silent default).
		cfg, cerr := config.Load(os.Getenv("KACHO_VPC_CONFIG_PATH"))
		if cerr != nil {
			return nil, fmt.Errorf("dsn unset (--dsn / %s) and vpc config load failed: %w", envDSN, cerr)
		}
		dsn = cfg.MigrateDSN()
	}

	return migrator.New(migrator.Config{
		Dialect:       dialect,
		DSN:           dsn,
		FS:            migrationsFS,
		MigrationsDir: defaultMigrationsDir,
	})
}
