package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all runtime configuration for the service.
type Config struct {
	Server       ServerConfig       `mapstructure:"server"`
	Admin        AdminConfig        `mapstructure:"admin"`
	OpenPay      OpenPayConfig      `mapstructure:"openpay"`
	Database     DatabaseConfig     `mapstructure:"database"`
	Redis        RedisConfig        `mapstructure:"redis"`
	Kafka        KafkaConfig        `mapstructure:"kafka"`
	Disbursement DisbursementConfig `mapstructure:"disbursement"`
	Webhook      WebhookConfig      `mapstructure:"webhook"`
	Telemetry    TelemetryConfig    `mapstructure:"telemetry"`
	Encryption   EncryptionConfig   `mapstructure:"encryption"`
}

type ServerConfig struct {
	GRPCPort    int    `mapstructure:"grpc_port"`
	HTTPPort    int    `mapstructure:"http_port"`
	TLSCertFile string `mapstructure:"tls_cert_file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"`
	// Maximum time a unary RPC may take.
	RequestTimeoutSec int `mapstructure:"request_timeout_sec"`
	// CheckoutBaseURL is the public URL of the hosted checkout service.
	// Subscription link checkout URLs are built as CheckoutBaseURL + "/s/" + token.
	CheckoutBaseURL string `mapstructure:"checkout_base_url"`
}

// OpenPayConfig holds the service-owner's single OpenPay merchant credentials.
//
// There is ONE merchant account for the entire platform. All customer charges
// land in this account's balance. The scheduler sends payouts from this balance
// to each tenant's registered bank account (CLABE) via SPEI.
//
// IMPORTANT: MerchantID, PrivateKey, and WebhookIngressSecret must be supplied
// via environment variables (OPENPAY_OPENPAY_PRIVATE_KEY, etc.) and never
// hard-coded in config.yaml.
type OpenPayConfig struct {
	Environment    string `mapstructure:"environment"` // "sandbox" | "production"
	SandboxBaseURL string `mapstructure:"sandbox_base_url"`
	ProdBaseURL    string `mapstructure:"prod_base_url"`
	HTTPTimeoutMS  int    `mapstructure:"http_timeout_ms"`
	MaxRetries     int    `mapstructure:"max_retries"`
	// ForwardClientIP must always be true per OpenPay anti-fraud requirement.
	ForwardClientIP bool `mapstructure:"forward_client_ip"`

	// Service-owner merchant credentials — load from env, never from YAML file.
	MerchantID           string `mapstructure:"merchant_id"`
	PrivateKey           string `mapstructure:"private_key"`            // HTTP Basic auth username
	PublicKey            string `mapstructure:"public_key"`             // used client-side only (JS SDK)
	WebhookIngressSecret string `mapstructure:"webhook_ingress_secret"` // validates OpenPay → our ingress
}

func (o OpenPayConfig) BaseURL() string {
	if o.Environment == "production" {
		return o.ProdBaseURL
	}
	return o.SandboxBaseURL
}

// Validate returns an error if required OpenPay credentials are missing.
func (o OpenPayConfig) Validate() error {
	if o.MerchantID == "" {
		return fmt.Errorf("openpay.merchant_id is required (set OPENPAY_OPENPAY_MERCHANT_ID)")
	}
	if o.PrivateKey == "" {
		return fmt.Errorf("openpay.private_key is required (set OPENPAY_OPENPAY_PRIVATE_KEY)")
	}
	return nil
}

type DatabaseConfig struct {
	DSN          string        `mapstructure:"dsn"`
	MaxOpenConns int           `mapstructure:"max_open_conns"`
	MaxIdleConns int           `mapstructure:"max_idle_conns"`
	ConnMaxLife  time.Duration `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type KafkaConfig struct {
	Brokers       []string `mapstructure:"brokers"`
	ConsumerGroup string   `mapstructure:"consumer_group"`

	// Topic names
	TopicPaymentEvents        string `mapstructure:"topic_payment_events"`
	TopicSubscriptionEvents   string `mapstructure:"topic_subscription_events"`
	TopicDisbursementCommands string `mapstructure:"topic_disbursement_commands"`
	TopicWebhookOutbound      string `mapstructure:"topic_webhook_outbound"`
	TopicWebhookDLQ           string `mapstructure:"topic_webhook_dlq"`
	TopicAuditEvents          string `mapstructure:"topic_audit_events"`
}

type DisbursementConfig struct {
	DefaultFrequency string `mapstructure:"default_frequency"`  // daily | weekly | monthly
	DefaultCron      string `mapstructure:"default_cron"`       // e.g. "0 18 * * *"
	MinIntervalHours int    `mapstructure:"min_interval_hours"` // minimum custom interval
}

type WebhookConfig struct {
	DispatchTimeoutMS int   `mapstructure:"dispatch_timeout_ms"`
	MaxAttempts       int   `mapstructure:"max_attempts"`
	IntervalsSec      []int `mapstructure:"retry_intervals_sec"`
}

type TelemetryConfig struct {
	JaegerEndpoint string `mapstructure:"jaeger_endpoint"`
	PrometheusPort int    `mapstructure:"prometheus_port"`
	LogLevel       string `mapstructure:"log_level"` // trace | debug | info | warn | error
	ServiceName    string `mapstructure:"service_name"`
}

// EncryptionConfig holds the AES key used to encrypt sensitive values at rest
// (webhook subscription secrets, tenant CLABE numbers).
// The OpenPay private key itself is never written to the DB — it lives only in
// this process's memory, loaded from the environment at startup.

// AdminConfig holds the static admin API key used to authenticate back-office
// operations (tenant CRUD). Set via OPENPAY_ADMIN_API_KEY environment variable.
// If empty, all AdminTenantService RPCs return Unauthenticated.
type AdminConfig struct {
	APIKey string `mapstructure:"api_key"`
}

type EncryptionConfig struct {
	// AES-256 key in hex (32 bytes = 64 hex chars).
	// Generate with: openssl rand -hex 32
	// In production, load from HashiCorp Vault or AWS Secrets Manager.
	AESKeyHex string `mapstructure:"aes_key_hex"`
}

// Load reads configuration from the file at cfgPath and then applies any
// OPENPAY_* environment variable overrides.
func Load(cfgPath string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.grpc_port", 50051)
	v.SetDefault("server.http_port", 8080)
	v.SetDefault("server.request_timeout_sec", 30)
	v.SetDefault("server.checkout_base_url", "http://localhost:3000")

	v.SetDefault("openpay.environment", "sandbox")
	v.SetDefault("openpay.sandbox_base_url", "https://sandbox-api.openpay.mx/v1")
	v.SetDefault("openpay.prod_base_url", "https://api.openpay.mx/v1")
	v.SetDefault("openpay.http_timeout_ms", 10000)
	v.SetDefault("openpay.max_retries", 3)
	v.SetDefault("openpay.forward_client_ip", true)

	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", "5m")

	v.SetDefault("kafka.consumer_group", "openpay-smart-workers")
	v.SetDefault("kafka.topic_payment_events", "payment.events")
	v.SetDefault("kafka.topic_subscription_events", "subscription.events")
	v.SetDefault("kafka.topic_disbursement_commands", "disbursement.commands")
	v.SetDefault("kafka.topic_webhook_outbound", "webhook.outbound")
	v.SetDefault("kafka.topic_webhook_dlq", "webhook.dlq")
	v.SetDefault("kafka.topic_audit_events", "audit.events")

	v.SetDefault("disbursement.default_frequency", "daily")
	v.SetDefault("disbursement.default_cron", "0 18 * * *")
	v.SetDefault("disbursement.min_interval_hours", 1)

	v.SetDefault("webhook.dispatch_timeout_ms", 5000)
	v.SetDefault("webhook.max_attempts", 7)
	v.SetDefault("webhook.retry_intervals_sec", []int{5, 30, 120, 600, 3600, 21600, 86400})

	v.SetDefault("telemetry.log_level", "info")
	v.SetDefault("telemetry.prometheus_port", 9090)
	v.SetDefault("telemetry.service_name", "openpay-smart-service")

	// File
	if cfgPath != "" {
		v.SetConfigFile(cfgPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}

	// Environment overrides: OPENPAY_DATABASE_DSN → database.dsn
	v.SetEnvPrefix("OPENPAY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Explicitly bind keys that have no default so AutomaticEnv picks them up.
	for _, key := range []string{
		"openpay.merchant_id",
		"openpay.private_key",
		"openpay.public_key",
		"openpay.webhook_ingress_secret",
		"database.dsn",
		"redis.addr",
		"redis.password",
		"kafka.brokers",
		"encryption.aes_key_hex",
		"admin.api_key",
	} {
		_ = v.BindEnv(key)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.OpenPay.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
