package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// Config contains settings shared by the API and background processes.
type Config struct {
	App     App     `mapstructure:"app"`
	DB      DB      `mapstructure:"db"`
	Cache   Cache   `mapstructure:"cache"`
	Sync    Sync    `mapstructure:"sync"`
	Redis   Redis   `mapstructure:"redis"`
	Crypto  Crypto  `mapstructure:"crypto"`
	JWT     JWT     `mapstructure:"jwt"`
	Storage Storage `mapstructure:"storage"`
	Worker  Worker  `mapstructure:"worker"`
	Refresh Refresh `mapstructure:"refresh"`
	Gateway Gateway `mapstructure:"gateway"`
	Shopee  Shopee  `mapstructure:"shopee"`
	Log     Log     `mapstructure:"log"`
}

// App contains process and HTTP server settings.
type App struct {
	Env            string        `mapstructure:"env"`
	Name           string        `mapstructure:"name"`
	HTTPAddr       string        `mapstructure:"http_addr"`
	PublicURL      string        `mapstructure:"public_url"`
	TrustedProxies []string      `mapstructure:"trusted_proxies"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
}

// DB contains PostgreSQL connection settings.
type DB struct {
	DSN             string        `mapstructure:"dsn"`
	MaxOpen         int           `mapstructure:"max_open"`
	MaxIdle         int           `mapstructure:"max_idle"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	TxnTimeout      time.Duration `mapstructure:"txn_timeout"`
	MigrateTimeout  time.Duration `mapstructure:"migrate_timeout"`
}

// Redis contains Redis connection settings.
type Redis struct {
	Addr         string `mapstructure:"addr"`
	Password     string `mapstructure:"password"`
	DB           int    `mapstructure:"db"`
	PoolSize     int    `mapstructure:"pool_size"`
	MinIdleConns int    `mapstructure:"min_idle_conns"`
}

// Crypto contains application encryption settings.
type Crypto struct {
	MasterKey  string `mapstructure:"master_key"`
	KeyVersion int    `mapstructure:"key_version"`
}

// JWT contains token signing and lifetime settings.
type JWT struct {
	Secret     string        `mapstructure:"secret"`
	AccessTTL  time.Duration `mapstructure:"access_ttl"`
	RefreshTTL time.Duration `mapstructure:"refresh_ttl"`
}

// Storage contains object storage settings.
type Storage struct {
	Endpoint  string `mapstructure:"endpoint"`
	Bucket    string `mapstructure:"bucket"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

// Worker contains background worker settings.
type Worker struct {
	Concurrency int      `mapstructure:"concurrency"`
	Queues      []string `mapstructure:"queues"`
}

// Shopee requires explicit configuration; fixtures use a local httptest endpoint.
type Shopee struct {
	BaseURL string `mapstructure:"base_url"`
}

// Log contains structured logger settings.
type Log struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load returns configuration using defaults, the optional file, then CH_ variables.
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetEnvPrefix("CH")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.AllowEmptyEnv(true)
	if err := bindEnvironment(v); err != nil {
		return nil, fmt.Errorf("bind configuration environment: %w", err)
	}
	if strings.TrimSpace(path) != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("configuration file cannot be read or parsed")
		}
	}

	normalizeTimeouts(v)
	var cfg Config
	if err := decodeFields(v, reflect.ValueOf(&cfg).Elem(), ""); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks required settings without including their values in errors.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("configuration is required")
	}
	if c.App.PublicURL != "" {
		u, err := url.Parse(c.App.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("app.public_url must be an HTTP(S) origin or base path without credentials, query or fragment")
		}
	}
	if strings.TrimSpace(c.DB.DSN) == "" {
		return fmt.Errorf("db.dsn is required")
	}
	if err := validateDSN(c.DB.DSN); err != nil {
		return err
	}
	if !validMasterKey(c.Crypto.MasterKey) {
		return fmt.Errorf("crypto.master_key must be base64 encoding of 32 bytes")
	}
	if strings.EqualFold(c.App.Env, "prod") && utf8.RuneCountInString(strings.TrimSpace(c.JWT.Secret)) < 32 {
		return fmt.Errorf("jwt.secret must contain at least 32 characters in prod")
	}
	return nil
}

func setDefaults(v *viper.Viper) {
	defaults := map[string]any{
		"app.env": "dev", "app.name": "commercehub", "app.http_addr": ":8080",
		"app.read_timeout": "30s", "app.write_timeout": "30s",
		"app.trusted_proxies": []string{},
		"db.max_open":         50, "db.max_idle": 10, "db.conn_max_lifetime": "30m",
		"db.txn_timeout": DefaultTxnTimeout.String(), "cache.op_timeout": DefaultCacheOpTimeout.String(),
		"db.migrate_timeout":        DefaultMigrateTimeout.String(),
		"sync.replay_batch_timeout": DefaultReplayBatchTimeout.String(),
		"redis.addr":                "localhost:6379", "redis.password": "", "redis.db": 0,
		"redis.pool_size": 10, "redis.min_idle_conns": 1,
		"crypto.key_version": 1, "storage.endpoint": "http://localhost:9000",
		"storage.bucket": "commercehub", "storage.access_key": "", "storage.secret_key": "",
		"storage.use_ssl": false, "jwt.secret": "", "jwt.access_ttl": "15m",
		"jwt.refresh_ttl": "168h", "worker.concurrency": 10,
		"worker.queues":     []string{"sync", "refresh", "billing", "notify"},
		"refresh.lead_time": "5m", "refresh.scan_interval": "1m", "refresh.concurrency": 1,
		"gateway.global_qps": 5000, "gateway.global_burst": 10000,
		"gateway.client_qps": 10, "gateway.client_burst": 20,
		"gateway.route_qps": 1000, "gateway.route_burst": 2000,
		"log.level": "info", "log.format": "json",
	}
	for key, value := range defaults {
		v.SetDefault(key, value)
	}
}

func bindEnvironment(v *viper.Viper) error {
	keys := []string{
		"app.env", "app.name", "app.http_addr", "app.public_url", "app.read_timeout", "app.write_timeout", "app.trusted_proxies",
		"db.dsn", "db.max_open", "db.max_idle", "db.conn_max_lifetime",
		"db.txn_timeout", "db.migrate_timeout", "cache.op_timeout",
		"sync.replay_batch_timeout",
		"redis.addr", "redis.password", "redis.db", "crypto.master_key", "crypto.key_version",
		"redis.pool_size", "redis.min_idle_conns",
		"jwt.secret", "jwt.access_ttl", "jwt.refresh_ttl", "storage.endpoint", "storage.bucket",
		"storage.access_key", "storage.secret_key", "storage.use_ssl", "worker.concurrency",
		"worker.queues", "log.level", "log.format",
		"refresh.lead_time", "refresh.scan_interval", "refresh.concurrency",
		"gateway.global_qps", "gateway.global_burst", "gateway.client_qps", "gateway.client_burst", "gateway.route_qps", "gateway.route_burst",
		"shopee.base_url",
	}
	for _, key := range keys {
		if err := v.BindEnv(key); err != nil {
			return err
		}
	}
	return nil
}

func validateDSN(dsn string) error {
	parsed, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		DisableAutomaticPing: true, Logger: gormlog.Default.LogMode(gormlog.Silent),
	})
	if err != nil {
		return fmt.Errorf("db.dsn must be a parseable PostgreSQL DSN")
	}
	pool, err := parsed.DB()
	if err != nil {
		return fmt.Errorf("db.dsn cannot initialize a PostgreSQL pool")
	}
	if err := pool.Close(); err != nil {
		return fmt.Errorf("db.dsn validation could not release the PostgreSQL pool")
	}
	return nil
}

func validMasterKey(value string) bool {
	encoded := strings.TrimPrefix(value, "base64:")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == 32
}

func decodeFields(v *viper.Viper, target reflect.Value, prefix string) error {
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		key := prefix + target.Type().Field(i).Tag.Get("mapstructure")
		if field.Kind() == reflect.Struct {
			if err := decodeFields(v, field, key+"."); err != nil {
				return err
			}
			continue
		}
		if err := v.UnmarshalKey(key, field.Addr().Interface()); err != nil {
			return fmt.Errorf("%s has an invalid configuration value", key)
		}
	}
	return nil
}
