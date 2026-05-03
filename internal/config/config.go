package config

import (
	"fmt"

	corecfg "github.com/PRO-Robotech/kacho-corelib/config"
)

// Config — конфигурация kacho-vpc.
type Config struct {
	DBHost     string `envconfig:"KACHO_VPC_DB_HOST" default:"localhost"`
	DBPort     string `envconfig:"KACHO_VPC_DB_PORT" default:"5432"`
	DBUser     string `envconfig:"KACHO_VPC_DB_USER" default:"vpc"`
	DBPassword string `envconfig:"KACHO_VPC_DB_PASSWORD" required:"true"`
	DBName     string `envconfig:"KACHO_VPC_DB_NAME" default:"kacho_vpc"`
	GrpcPort   string `envconfig:"KACHO_VPC_GRPC_PORT" default:"9090"`

	ResourceManagerGRPCAddr string `envconfig:"KACHO_VPC_RESOURCE_MANAGER_GRPC_ADDR" default:"resource-manager.kacho.svc.cluster.local:9090"`
}

// DSN возвращает PostgreSQL DSN строку.
func (c Config) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName,
	)
}

// Load загружает конфигурацию из переменных окружения.
func Load() (Config, error) {
	var c Config
	err := corecfg.Load(&c)
	return c, err
}
