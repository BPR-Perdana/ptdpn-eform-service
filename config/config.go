// Package config handles loading and validating all application configuration
// from environment variables and .env files using the Viper library.
//
// HOW IT WORKS:
//   - In development: reads from a .env file in the project root
//   - In production:  reads from real OS environment variables
//   - Viper handles both transparently — no code changes needed between envs
//
// USAGE (from anywhere in the app):
//
//	cfg, err := config.Load()
//	if err != nil { ... }
//	fmt.Println(cfg.Database.Host)
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration struct.
type Config struct {
	App         AppConfig
	Database    DatabaseConfig
	Redis       RedisConfig
	JWT         JWTConfig
	Session     SessionConfig
	Storage     StorageConfig
	Vida        VidaConfig
	Email       EmailConfig
	WhatsApp    WhatsAppConfig
	Rate        RateLimitConfig
	Log         LogConfig
	IOH         IOHConfig
	FraudPoller FraudPollerConfig
}

// AppConfig holds general application settings.
type AppConfig struct {
	Name        string   // APP_NAME
	Env         string   // APP_ENV: development | staging | production
	Port        int      // APP_PORT
	BaseURL     string   // APP_BASE_URL
	FrontendURL string   // FRONTEND_URL — base URL customer frontend (untuk link agreement email)
	CORSOrigins []string // CORS_ALLOWED_ORIGINS (comma-separated)
}

// DatabaseConfig holds MySQL connection parameters.
type DatabaseConfig struct {
	Host            string        // DB_HOST
	Port            int           // DB_PORT
	Name            string        // DB_NAME
	User            string        // DB_USER
	Password        string        // DB_PASSWORD
	MaxOpenConns    int           // DB_MAX_OPEN_CONNS
	MaxIdleConns    int           // DB_MAX_IDLE_CONNS
	ConnMaxLifetime time.Duration // DB_CONN_MAX_LIFETIME
}

// DSN builds the MySQL Data Source Name string.
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.Name,
	)
}

// RedisConfig holds Redis connection parameters.
type RedisConfig struct {
	Host     string // REDIS_HOST
	Port     int    // REDIS_PORT
	Password string // REDIS_PASSWORD
	DB       int    // REDIS_DB
	PoolSize int    // REDIS_POOL_SIZE
}

// Addr returns the Redis address in host:port format.
func (r RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

// JWTConfig holds settings for RS256 JWT tokens used by internal staff.
type JWTConfig struct {
	PrivateKeyPath  string        // JWT_PRIVATE_KEY_PATH
	PublicKeyPath   string        // JWT_PUBLIC_KEY_PATH
	AccessTokenTTL  time.Duration // JWT_ACCESS_TOKEN_TTL
	RefreshTokenTTL time.Duration // JWT_REFRESH_TOKEN_TTL
}

// SessionConfig holds settings for customer session tokens.
type SessionConfig struct {
	SecretKey  string // SESSION_SECRET_KEY
	TTLMinutes int    // SESSION_TTL_MINUTES
}

// StorageConfig holds local file storage settings.
type StorageConfig struct {
	BasePath            string // STORAGE_BASE_PATH
	LogoPath            string // STORAGE_LOGO_PATH — path ke file logo perusahaan
	MaxKTPSizeMB        int    // STORAGE_MAX_KTP_SIZE_MB
	MaxCollateralSizeMB int    // STORAGE_MAX_COLLATERAL_SIZE_MB
	MaxContractSizeMB   int    // STORAGE_MAX_CONTRACT_SIZE_MB
}

// ─── VIDA Configuration ───────────────────────────────────────────────────────
//
// VIDA menyediakan 3 layanan dengan credential dan base URL yang berbeda:
//
//  1. OCR + Fraud Mitigation → credential sama (VidaServiceConfig)
//     SSO:  https://qa-sso.vida.id/auth/realms/vida/protocol/openid-connect/token
//     Base: https://services-sandbox.vida.id
//     Scope: roles
//
//  2. PoA eSignature → credential berbeda (VidaSignConfig)
//     SSO:  https://qa-sso.vida.id/auth/realms/vida/protocol/openid-connect/token
//     Base: https://services-sandbox.vida.id
//     Scope: roles
//
//  3. eMeterai → SSO dan base URL berbeda (VidaeMateraiConfig)
//     SSO:  https://qa-sso-26.np.vida.id/auth/realms/vida/protocol/openid-connect/token
//     Base: https://sandbox-stamp-gateway.np.vida.id
//     Scope: openid
//     Header: X-PARTNER-ID

// VidaConfig groups all VIDA service configurations.
type VidaConfig struct {
	OCR        VidaServiceConfig // OCR + Fraud Mitigation (same credential)
	Sign       VidaSignConfig
	EMaterai   VidaeMateraiConfig
	WebSDK     VidaWebSDKConfig
	DirectSign DirectSignConfig

	SigningKey    string
	WebhookSecret string        // VIDA_WEBHOOK_SECRET
	HTTPTimeout   time.Duration // VIDA_HTTP_TIMEOUT
	MockFraud     bool
	MockContract  bool // VIDA_FRAUD_MOCK — jika true, bypass panggilan Fraud Mitigation dan kembalikan hasil mock
}

// DirectSignConfig holds credentials for VIDA Direct Sign Platform API.
type DirectSignConfig struct {
	BaseURL      string // VIDA_DSIGN_BASE_URL
	ClientID     string // VIDA_DSIGN_CLIENT_ID
	SecretKey    string // VIDA_DSIGN_SECRET_KEY
	CreatorEmail string // VIDA_DSIGN_CREATOR_EMAIL — email member workspace VIDA
}

// VidaServiceConfig adalah credential untuk OCR dan Fraud Mitigation API.
// Kedua service ini menggunakan client_id dan secret_key yang sama.
type VidaServiceConfig struct {
	BaseURL   string // VIDA_OCR_BASE_URL
	ClientID  string // VIDA_OCR_CLIENT_ID
	SecretKey string // VIDA_OCR_SECRET_KEY
}

type VidaWebSDKConfig struct {
	ClientID  string // VIDA_WEB_SDK_CLIENT_ID
	SecretKey string // VIDA_WEB_SDK_SECRET_KEY
}

// VidaSignConfig holds credentials for the PoA eSignature API.
//
// encCVV generation (AES/CFB/NoPadding):
//
//	plaintext = CVV + epochSeconds   e.g. "049" + "1706605635" = "0491706605635"
//	key       = SecretKey hex-decoded (32 bytes for AES-256)
//	IV        = APIKey as UTF-8 bytes (must be exactly 16 bytes)
//	output    = URL-safe base64 without padding
type VidaSignConfig struct {
	BaseURL      string // VIDA_SIGN_BASE_URL
	ClientID     string // VIDA_SIGN_CLIENT_ID
	SecretKey    string // VIDA_SIGN_SECRET_KEY — hex string, AES-256 key + OAuth2 client secret
	APIKey       string // VIDA_SIGN_API_KEY    — 16 chars, used as AES IV
	CVV          string // VIDA_SIGN_CVV            — clear text CVV (3 digit)
	CVVSecretKey string // VIDA_SIGN_CVV_SECRET_KEY — 64 hex chars, khusus enkripsi CVV
	KeyID        string // VIDA_SIGN_KEY_ID     — POA ID from VIDA email
}

// VidaeMateraiConfig holds credentials for the eMeterai stamping API.
//
// PENTING: eMeterai menggunakan SSO yang BERBEDA dari service VIDA lainnya.
// Pastikan menggunakan endpoint qa-sso-26 dan scope "openid".
type VidaeMateraiConfig struct {
	BaseURL   string // VIDA_EMETERAI_BASE_URL
	ClientID  string // VIDA_EMETERAI_CLIENT_ID
	SecretKey string // VIDA_EMETERAI_SECRET_KEY
	PartnerID string // VIDA_EMETERAI_PARTNER_ID — nilai untuk header X-PARTNER-ID
}

// ─── Notification Configuration ───────────────────────────────────────────────

// EmailConfig holds settings for email notifications.
type EmailConfig struct {
	Provider         string // EMAIL_PROVIDER: "smtp" (default) or "graph"
	Host             string // SMTP_HOST
	Port             int    // SMTP_PORT
	Username         string // SMTP_USERNAME
	Password         string // SMTP_PASSWORD
	FromName         string // SMTP_FROM_NAME
	FromEmail        string // SMTP_FROM_EMAIL
	GraphTenantID    string // GRAPH_TENANT_ID
	GraphClientID    string // GRAPH_CLIENT_ID
	GraphClientSecret string // GRAPH_CLIENT_SECRET
}

// WhatsAppConfig holds settings for WhatsApp notifications.
type WhatsAppConfig struct {
	Provider   string // WA_PROVIDER
	APIURL     string // WA_API_URL
	APIToken   string // WA_API_TOKEN
	FromNumber string // WA_FROM_NUMBER
}

// RateLimitConfig holds settings for API rate limiting.
type RateLimitConfig struct {
	Requests int           // RATE_LIMIT_REQUESTS
	Window   time.Duration // RATE_LIMIT_WINDOW
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level  string // LOG_LEVEL: debug | info | warn | error
	Format string // LOG_FORMAT: console | json
	Output string // LOG_OUTPUT: stdout | file path
}

type IOHConfig struct {
	SMSURL   string
	Username string
	Password string
	SenderID string
}

type FraudPollerConfig struct {
	Interval time.Duration
}

// ─────────────────────────────────────────────────────────────────────────────

// Load reads configuration from environment variables (or .env file in dev).
// Call this once at startup in main.go and inject via dependency injection.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	v.SetConfigFile(".env")
	v.SetConfigType("env")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			if !strings.Contains(err.Error(), "no such file") {
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
		}
	}

	cfg := &Config{}

	// ── Application ──────────────────────────────────────────────────────────
	cfg.App = AppConfig{
		Name:        v.GetString("APP_NAME"),
		Env:         v.GetString("APP_ENV"),
		Port:        v.GetInt("APP_PORT"),
		BaseURL:     v.GetString("APP_BASE_URL"),
		FrontendURL: v.GetString("FRONTEND_URL"),
	}
	if raw := v.GetString("CORS_ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				cfg.App.CORSOrigins = append(cfg.App.CORSOrigins, trimmed)
			}
		}
	}

	// ── Database ─────────────────────────────────────────────────────────────
	cfg.Database = DatabaseConfig{
		Host:            v.GetString("DB_HOST"),
		Port:            v.GetInt("DB_PORT"),
		Name:            v.GetString("DB_NAME"),
		User:            v.GetString("DB_USER"),
		Password:        v.GetString("DB_PASSWORD"),
		MaxOpenConns:    v.GetInt("DB_MAX_OPEN_CONNS"),
		MaxIdleConns:    v.GetInt("DB_MAX_IDLE_CONNS"),
		ConnMaxLifetime: v.GetDuration("DB_CONN_MAX_LIFETIME"),
	}

	// ── Redis ────────────────────────────────────────────────────────────────
	cfg.Redis = RedisConfig{
		Host:     v.GetString("REDIS_HOST"),
		Port:     v.GetInt("REDIS_PORT"),
		Password: v.GetString("REDIS_PASSWORD"),
		DB:       v.GetInt("REDIS_DB"),
		PoolSize: v.GetInt("REDIS_POOL_SIZE"),
	}

	// ── JWT ──────────────────────────────────────────────────────────────────
	cfg.JWT = JWTConfig{
		PrivateKeyPath:  v.GetString("JWT_PRIVATE_KEY_PATH"),
		PublicKeyPath:   v.GetString("JWT_PUBLIC_KEY_PATH"),
		AccessTokenTTL:  v.GetDuration("JWT_ACCESS_TOKEN_TTL"),
		RefreshTokenTTL: v.GetDuration("JWT_REFRESH_TOKEN_TTL"),
	}

	// ── Customer Session ─────────────────────────────────────────────────────
	cfg.Session = SessionConfig{
		SecretKey:  v.GetString("SESSION_SECRET_KEY"),
		TTLMinutes: v.GetInt("SESSION_TTL_MINUTES"),
	}

	// ── Storage ──────────────────────────────────────────────────────────────
	cfg.Storage = StorageConfig{
		BasePath:            v.GetString("STORAGE_BASE_PATH"),
		LogoPath:            v.GetString("STORAGE_LOGO_PATH"),
		MaxKTPSizeMB:        v.GetInt("STORAGE_MAX_KTP_SIZE_MB"),
		MaxCollateralSizeMB: v.GetInt("STORAGE_MAX_COLLATERAL_SIZE_MB"),
		MaxContractSizeMB:   v.GetInt("STORAGE_MAX_CONTRACT_SIZE_MB"),
	}

	// ── VIDA ─────────────────────────────────────────────────────────────────
	cfg.Vida = VidaConfig{
		// OCR + Fraud Mitigation: credential sama
		OCR: VidaServiceConfig{
			BaseURL:   v.GetString("VIDA_OCR_BASE_URL"),
			ClientID:  v.GetString("VIDA_OCR_CLIENT_ID"),
			SecretKey: v.GetString("VIDA_OCR_SECRET_KEY"),
		},
		Sign: VidaSignConfig{
			BaseURL:      v.GetString("VIDA_SIGN_BASE_URL"),
			ClientID:     v.GetString("VIDA_SIGN_CLIENT_ID"),
			SecretKey:    v.GetString("VIDA_SIGN_SECRET_KEY"),
			APIKey:       v.GetString("VIDA_SIGN_API_KEY"),
			CVV:          v.GetString("VIDA_SIGN_CVV"),
			CVVSecretKey: v.GetString("VIDA_SIGN_CVV_SECRET_KEY"),
			KeyID:        v.GetString("VIDA_SIGN_KEY_ID"),
		},
		EMaterai: VidaeMateraiConfig{
			BaseURL:   v.GetString("VIDA_EMETERAI_BASE_URL"),
			ClientID:  v.GetString("VIDA_EMETERAI_CLIENT_ID"),
			SecretKey: v.GetString("VIDA_EMETERAI_SECRET_KEY"),
			PartnerID: v.GetString("VIDA_EMETERAI_PARTNER_ID"),
		},
		WebSDK: VidaWebSDKConfig{ // ← TAMBAHKAN INI
			ClientID:  v.GetString("VIDA_WEB_SDK_CLIENT_ID"),
			SecretKey: v.GetString("VIDA_WEB_SDK_CLIENT_SECRET"),
		},
		DirectSign: DirectSignConfig{
			BaseURL:      v.GetString("VIDA_DSIGN_BASE_URL"),
			ClientID:     v.GetString("VIDA_DSIGN_CLIENT_ID"),
			SecretKey:    v.GetString("VIDA_DSIGN_SECRET_KEY"),
			CreatorEmail: v.GetString("VIDA_DSIGN_CREATOR_EMAIL"),
		},
		SigningKey:    v.GetString("VIDA_SIGNING_KEY"),
		WebhookSecret: v.GetString("VIDA_WEBHOOK_SECRET"),
		HTTPTimeout:   v.GetDuration("VIDA_HTTP_TIMEOUT"),
		MockFraud:     v.GetBool("VIDA_FRAUD_MOCK"),
		MockContract:  v.GetBool("VIDA_CONTRACT_MOCK"),
	}

	// ── Email ────────────────────────────────────────────────────────────────
	cfg.Email = EmailConfig{
		Provider:          v.GetString("EMAIL_PROVIDER"),
		Host:              v.GetString("SMTP_HOST"),
		Port:              v.GetInt("SMTP_PORT"),
		Username:          v.GetString("SMTP_USERNAME"),
		Password:          v.GetString("SMTP_PASSWORD"),
		FromName:          v.GetString("SMTP_FROM_NAME"),
		FromEmail:         v.GetString("SMTP_FROM_EMAIL"),
		GraphTenantID:     v.GetString("GRAPH_TENANT_ID"),
		GraphClientID:     v.GetString("GRAPH_CLIENT_ID"),
		GraphClientSecret: v.GetString("GRAPH_CLIENT_SECRET"),
	}
	if cfg.Email.Provider == "" {
		cfg.Email.Provider = "smtp" // Default fallback
	}

	// ── WhatsApp ─────────────────────────────────────────────────────────────
	cfg.WhatsApp = WhatsAppConfig{
		Provider:   v.GetString("WA_PROVIDER"),
		APIURL:     v.GetString("WA_API_URL"),
		APIToken:   v.GetString("WA_API_TOKEN"),
		FromNumber: v.GetString("WA_FROM_NUMBER"),
	}

	// ── Rate Limiting ─────────────────────────────────────────────────────────
	cfg.Rate = RateLimitConfig{
		Requests: v.GetInt("RATE_LIMIT_REQUESTS"),
		Window:   v.GetDuration("RATE_LIMIT_WINDOW"),
	}

	// ── Logging ──────────────────────────────────────────────────────────────
	cfg.Log = LogConfig{
		Level:  v.GetString("LOG_LEVEL"),
		Format: v.GetString("LOG_FORMAT"),
		Output: v.GetString("LOG_OUTPUT"),
	}

	cfg.IOH = IOHConfig{
		SMSURL:   v.GetString("IOH_SMS_URL"),
		Username: v.GetString("IOH_SMS_USERNAME"),
		Password: v.GetString("IOH_SMS_PASSWORD"),
		SenderID: v.GetString("IOH_SMS_SENDER_ID"),
	}
	cfg.FraudPoller = FraudPollerConfig{
		Interval: v.GetDuration("FRAUD_POLL_INTERVAL"),
	}
	if cfg.FraudPoller.Interval == 0 {
		cfg.FraudPoller.Interval = 30 * time.Minute // default
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks that critical configuration values are present.
// Fail fast at startup rather than failing during a customer request.
func validate(cfg *Config) error {
	required := []struct {
		value string
		name  string
	}{
		// Database
		{cfg.Database.Host, "DB_HOST"},
		{cfg.Database.User, "DB_USER"},
		{cfg.Database.Password, "DB_PASSWORD"},
		{cfg.Database.Name, "DB_NAME"},
		// Auth
		{cfg.JWT.PrivateKeyPath, "JWT_PRIVATE_KEY_PATH"},
		{cfg.JWT.PublicKeyPath, "JWT_PUBLIC_KEY_PATH"},
		{cfg.Session.SecretKey, "SESSION_SECRET_KEY"},
		// Storage
		{cfg.Storage.BasePath, "STORAGE_BASE_PATH"},
		// VIDA
		{cfg.Vida.OCR.ClientID, "VIDA_OCR_CLIENT_ID"},
		{cfg.Vida.OCR.SecretKey, "VIDA_OCR_SECRET_KEY"},
		{cfg.Vida.Sign.ClientID, "VIDA_SIGN_CLIENT_ID"},
		{cfg.Vida.Sign.APIKey, "VIDA_SIGN_API_KEY"},
		{cfg.Vida.Sign.CVV, "VIDA_SIGN_CVV"},
		{cfg.Vida.Sign.CVVSecretKey, "VIDA_SIGN_CVV_SECRET_KEY"},
		{cfg.Vida.Sign.KeyID, "VIDA_SIGN_KEY_ID"},
		{cfg.Vida.EMaterai.ClientID, "VIDA_EMETERAI_CLIENT_ID"},
		{cfg.Vida.EMaterai.PartnerID, "VIDA_EMETERAI_PARTNER_ID"},
	}

	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return fmt.Errorf("required configuration '%s' is not set", r.name)
		}
	}

	validEnvs := map[string]bool{"development": true, "staging": true, "production": true}
	if cfg.App.Env != "" && !validEnvs[cfg.App.Env] {
		return fmt.Errorf("APP_ENV must be one of: development, staging, production (got: %s)", cfg.App.Env)
	}

	return nil
}

// IsProduction returns true when running in the production environment.
func (c *Config) IsProduction() bool {
	return c.App.Env == "production"
}

// IsDevelopment returns true when running in the development environment.
func (c *Config) IsDevelopment() bool {
	return c.App.Env == "development"
}
