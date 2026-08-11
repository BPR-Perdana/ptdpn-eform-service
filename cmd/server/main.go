// main.go is the entry point for the BPR Perdana E-Form Backend.
//
// ITS ONLY JOB is to wire dependencies together and start the server.
// All business logic belongs in services. All HTTP logic belongs in handlers.
// This file should stay thin — just orchestration.
//
// STARTUP SEQUENCE:
//  1. Load configuration from environment / .env file
//  2. Initialize logger (needed early so we can log startup errors)
//  3. Connect to MySQL database
//  4. Connect to Redis
//  5. Initialize all package-level dependencies (JWT, storage, etc.)
//  6. Initialize handlers
//  7. Set up the HTTP router with all routes
//  8. Start the server with graceful shutdown
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cappyHoding/ptdpn-eform-service/config"
	"github.com/cappyHoding/ptdpn-eform-service/internal/api/handler"
	"github.com/cappyHoding/ptdpn-eform-service/internal/api/router"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/ioh"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/vida"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/internal/service"
	"github.com/cappyHoding/ptdpn-eform-service/internal/worker"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/email"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/jwt"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/storage"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	// ── STEP 1: Load Configuration ────────────────────────────────────────────
	// This is the FIRST thing we do. If config fails, nothing else can work.
	cfg, err := config.Load()
	if err != nil {
		// We don't have a logger yet, so we print to stderr directly
		fmt.Fprintf(os.Stderr, "FATAL: failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// ── STEP 2: Initialize Logger ─────────────────────────────────────────────
	// Logger must be initialized before anything else so we can log all
	// subsequent startup steps with proper structure.
	log, err := logger.New(cfg.Log.Level, cfg.Log.Format, cfg.Log.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	// Flush any buffered log entries when main() returns
	defer log.Sync()

	log.Info("Starting BPR Perdana E-Form Backend",
		zap.String("env", cfg.App.Env),
		zap.String("version", "1.0.0"),
	)

	// ── STEP 3: Connect to MySQL ──────────────────────────────────────────────
	db, err := connectMySQL(cfg, log)
	if err != nil {
		log.Fatal("Failed to connect to MySQL", zap.Error(err))
	}
	log.Info("MySQL connected successfully",
		zap.String("host", cfg.Database.Host),
		zap.String("database", cfg.Database.Name),
	)

	// ── STEP 4: Connect to Redis ──────────────────────────────────────────────
	redisClient, err := connectRedis(cfg, log)
	if err != nil {
		log.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	log.Info("Redis connected successfully",
		zap.String("host", cfg.Redis.Host),
	)
	// Ensure the Redis connection is closed cleanly when the server stops
	defer redisClient.Close()

	// Suppress unused variable warning until we use redisClient in services
	_ = redisClient
	_ = db

	// ── STEP 5: Initialize Dependencies ──────────────────────────────────────

	// JWT Manager: loads RSA keys from disk for token signing/verification
	jwtManager, err := jwt.NewManager(
		cfg.JWT.PrivateKeyPath,
		cfg.JWT.PublicKeyPath,
		cfg.JWT.AccessTokenTTL,
		cfg.JWT.RefreshTokenTTL,
	)
	if err != nil {
		log.Fatal("Failed to initialize JWT manager", zap.Error(err))
	}
	log.Info("JWT manager initialized (RS256)")

	// Storage Manager: verifies storage directories exist and are writable
	storageManager, err := storage.New(cfg.Storage.BasePath)
	if err != nil {
		log.Fatal("Failed to initialize storage manager",
			zap.String("path", cfg.Storage.BasePath),
			zap.Error(err),
		)
	}
	log.Info("Storage manager initialized",
		zap.String("base_path", cfg.Storage.BasePath),
	)

	mailer := email.New(
		cfg.Email.Host,
		cfg.Email.Port,
		cfg.Email.Username,
		cfg.Email.Password,
		cfg.Email.FromName,
		cfg.Email.FromEmail,
	)

	if cfg.Storage.LogoPath != "" {
		if err := mailer.LoadLogo(cfg.Storage.LogoPath); err != nil {
			log.Warn("Email logo not loaded — emails will be sent without logo",
				zap.String("path", cfg.Storage.LogoPath),
				zap.Error(err),
			)
		} else {
			// log.Info("Email logo loaded", zap.String("path", cfg.Storage.LogoPath))
			log.Info("Email logo debug",
				zap.String("path", cfg.Storage.LogoPath),
				zap.Int("data_uri_length", len(mailer.LogoDataURI())), // kalau 0 = gagal
			)
		}
	}

	// IOH SMS Client
	smsClient := ioh.NewSMSClient(
		cfg.IOH.Username,
		cfg.IOH.Password,
		cfg.IOH.SenderID,
	)
	if cfg.IOH.Username != "" {
		log.Info("IOH SMS client initialized", zap.String("sender_id", cfg.IOH.SenderID))
	} else {
		log.Warn("IOH SMS credentials not set — OTP will fail")
	}

	// STEP 6: Initialize Repositories
	userRepo := repository.NewUserRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	customerRepo := repository.NewCustomerRepository(db)
	appRepo := repository.NewApplicationRepository(db)
	configRepo := repository.NewConfigRepository(db)
	contractRepo := repository.NewContractRepository(db)
	notifRepo := repository.NewNotificationRepository(db)
	log.Info("Repositories initialized")

	// STEP 7: Initialize Services
	vidaServices := vida.NewServices(cfg)
	log.Info("VIDA services initialized")

	otpSvc := service.NewOTPService(smsClient, redisClient, log)
	authSvc := service.NewAuthService(userRepo, auditRepo, jwtManager, log)
	notifSvc := service.NewNotificationService(mailer, notifRepo, log)
	appSvc := service.NewApplicationService(appRepo, customerRepo, auditRepo, vidaServices, storageManager, notifSvc, log, cfg)
	contractSvc := service.NewContractService(appRepo, contractRepo, auditRepo, vidaServices, storageManager, cfg.Storage.LogoPath, notifSvc, cfg, log)
	adminSvc := service.NewAdminService(appRepo, userRepo, auditRepo, configRepo, contractSvc, notifSvc, log)
	log.Info("Services initialized")

	// STEP 8: Initialize Handlers
	handlers := &handler.Registry{
		Application: handler.NewApplicationHandler(appSvc, contractSvc, otpSvc, log),
		Auth:        handler.NewAuthHandler(authSvc),
		Admin:       handler.NewAdminHandler(adminSvc),
		Webhook:     handler.NewWebhookHandler(contractSvc, contractRepo, cfg.Vida.WebhookSecret, log),
		Public:      handler.NewPublicHandler(configRepo),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fraudPollInterval := 30 * time.Minute
	if cfg.Vida.MockFraud {
		fraudPollInterval = 10 * time.Second
		log.Warn("VIDA_FRAUD_MOCK=true: fraud poller interval dipercepat ke 10 detik")
	}
	fraudPoller := worker.NewFraudPoller(appRepo, vidaServices, smsClient, fraudPollInterval, log)

	go fraudPoller.Start(ctx)
	log.Info("Fraud poller started", zap.Duration("interval", fraudPollInterval))

	// ── STEP 7: Set Up Router ─────────────────────────────────────────────────
	httpRouter := router.Setup(router.Dependencies{
		Config:   cfg,
		Logger:   log,
		JWT:      jwtManager,
		Handlers: handlers,
		AppRepo:  appRepo,
	})

	// ── STEP 8: Start HTTP Server with Graceful Shutdown ─────────────────────
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.App.Port),
		Handler: httpRouter,

		// Timeouts prevent slow clients from holding connections open forever.
		// These are critical for a public-facing API.
		ReadTimeout:    15 * time.Second, // Max time to read the full request
		WriteTimeout:   30 * time.Second, // Max time to write the full response
		IdleTimeout:    60 * time.Second, // Max time to keep idle connections alive
		MaxHeaderBytes: 1 << 20,          // 1MB max header size
	}

	// Start the server in a goroutine so the main goroutine can
	// listen for shutdown signals
	go func() {
		log.Info("Server listening",
			zap.String("address", srv.Addr),
			zap.String("base_url", cfg.App.BaseURL),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Server failed to start", zap.Error(err))
		}
	}()

	// ── GRACEFUL SHUTDOWN ─────────────────────────────────────────────────────
	// Wait for OS shutdown signal (SIGINT = Ctrl+C, SIGTERM = Docker/K8s stop)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Block here until a signal is received
	sig := <-quit
	log.Info("Shutdown signal received", zap.String("signal", sig.String()))

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("Server shutdown error", zap.Error(err))
	} else {
		log.Info("Server shut down gracefully")
	}
}

// connectMySQL establishes a GORM database connection with the configured pool settings.
func connectMySQL(cfg *config.Config, log *logger.Logger) (*gorm.DB, error) {
	// Configure GORM's internal logger based on our app environment
	// In development: log all SQL queries (useful for debugging)
	// In production: only log slow queries and errors
	var gormLogLevel gormlogger.LogLevel
	if cfg.IsDevelopment() {
		gormLogLevel = gormlogger.Info // Log all SQL
	} else {
		gormLogLevel = gormlogger.Warn // Only log slow queries and errors
	}

	db, err := gorm.Open(mysql.Open(cfg.Database.DSN()), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLogLevel),
		// DisableForeignKeyConstraintWhenMigrating: true, // Uncomment if you handle FK via migrations
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Get the underlying sql.DB to configure the connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Connection pool configuration
	// These values come from your .env file and should be tuned based on load
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	// Verify the connection is actually working
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// connectRedis establishes a Redis connection and verifies it with a PING.
func connectRedis(cfg *config.Config, log *logger.Logger) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return client, nil
}
