// Package router configures the Gin HTTP router with all API routes.
//
// ROUTE STRUCTURE:
//
//	/api/v1/
//	    health                         → health check (no auth)
//	    applications/                  → customer-facing endpoints (session auth)
//	    admin/                         → internal staff endpoints (JWT auth)
//	        auth/                      → login, logout, refresh (no auth needed)
//	        applications/              → review dashboard (operator, supervisor)
//	        users/                     → user management (admin only)
//	        config/                    → system config (admin only)
//	        audit-logs/                → compliance logs (admin, supervisor)
//	/webhooks/
//	    vida                           → VIDA webhook callbacks (HMAC verification)
package router

import (
	"net/http"

	"github.com/cappyHoding/ptdpn-eform-service/config"
	"github.com/cappyHoding/ptdpn-eform-service/internal/api/handler"
	"github.com/cappyHoding/ptdpn-eform-service/internal/api/middleware"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/jwt"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
)

// Dependencies groups all handler dependencies for clean injection.
type Dependencies struct {
	Config   *config.Config
	Logger   *logger.Logger
	JWT      *jwt.Manager
	Handlers *handler.Registry
	AppRepo  repository.ApplicationRepository
}

// Setup creates and configures the Gin router with all routes and middleware.
// Returns the configured router ready to be passed to http.Server.
func Setup(deps Dependencies) *gin.Engine {
	// Set Gin mode based on environment
	// In production: disables debug logging and color output
	if deps.Config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New() // gin.New() instead of gin.Default() — we add our own middleware

	// ── Global Middleware (runs on EVERY request) ──────────────────────────────

	// 1. Panic recovery — must be first so it wraps everything
	router.Use(middleware.Recovery(deps.Logger))

	// 2. Request ID — assigns a unique ID to each request for tracing
	//    The frontend can include this in bug reports
	router.Use(requestid.New())

	// 3. CORS — allows the React frontend to call our API from a different origin
	router.Use(cors.New(cors.Config{
		AllowOrigins: deps.Config.App.CORSOrigins,
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", "X-Session-Token"},
		// ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,         // We use tokens, not cookies
		MaxAge:           12 * 60 * 60, // 12 hours — how long browsers cache the preflight
	}))

	// 4. Request logger — logs every request after CORS
	router.Use(middleware.RequestLogger(deps.Logger))

	// ── Health Check ──────────────────────────────────────────────────────────
	// Simple endpoint for load balancers and monitoring to verify the server is up
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "bpr-perdana-eform",
		})
	})

	// ── API v1 Routes ─────────────────────────────────────────────────────────
	v1 := router.Group("/api/v1")

	// Public config (no auth)
	v1.GET("/config", deps.Handlers.Public.GetPublicConfig)

	// ── Customer-Facing Routes (no login, session-token auth after Step 2) ───
	setupCustomerRoutes(v1, deps)

	// ── Internal Admin Routes (JWT auth required) ─────────────────────────────
	setupAdminRoutes(v1, deps)

	// ── Webhook Routes ────────────────────────────────────────────────────────
	setupWebhookRoutes(router, deps)

	return router
}

// setupCustomerRoutes configures all public/customer-facing endpoints.
func setupCustomerRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	apps := v1.Group("/applications")
	h := deps.Handlers

	// Step 1 — Agreement (no auth)
	apps.POST("/agree", h.Application.AcceptAgreement)

	// Step 2 — Create application (no auth, returns session token)
	apps.POST("", h.Application.Create)

	apps.GET("/track", h.Application.TrackStatus)

	// Payment proof & eSign agreement — PUBLIC (no session needed)
	apps.POST("/:id/payment-proof", h.Application.UploadPaymentProof)
	apps.GET("/:id/esign-agreement", h.Application.GetESignAgreement)
	apps.POST("/:id/esign-tos", h.Application.AcceptESignTOS)

	// Steps 3-7 — Require valid X-Session-Token header
	// The session middleware validates the token and binds it to the application ID
	sessionRequired := apps.Group("")
	sessionRequired.Use(middleware.RequireCustomerSession(deps.AppRepo))
	{
		sessionRequired.GET("/:id", h.Application.GetByID)
		sessionRequired.POST("/:id/ocr", h.Application.SubmitOCR)                     // Step 3
		sessionRequired.PATCH("/:id/personal-info", h.Application.UpdatePersonalInfo) // Step 4
		sessionRequired.POST("/:id/otp/send", h.Application.SendOTP)
		sessionRequired.POST("/:id/otp/verify", h.Application.VerifyOTP)
		sessionRequired.GET("/:id/liveness/token", h.Application.GetLivenessToken)
		sessionRequired.POST("/:id/liveness", h.Application.SubmitLiveness)          // Step 5
		sessionRequired.PATCH("/:id/disbursement", h.Application.UpdateDisbursement) // Step 6
		sessionRequired.PATCH("/:id/collateral", h.Application.SubmitCollateral)
		sessionRequired.POST("/:id/submit", h.Application.Submit) // Step 7
	}
}

// setupAdminRoutes configures all internal staff endpoints.
func setupAdminRoutes(v1 *gin.RouterGroup, deps Dependencies) {
	admin := v1.Group("/admin")

	// Auth endpoints — no JWT required (you need to login first!)
	auth := admin.Group("/auth")
	{
		auth.POST("/login", deps.Handlers.Auth.Login)
		auth.POST("/refresh", deps.Handlers.Auth.RefreshToken)
		auth.POST("/logout", middleware.RequireInternalAuth(deps.JWT), deps.Handlers.Auth.Logout)
		auth.GET("/me", middleware.RequireInternalAuth(deps.JWT), deps.Handlers.Auth.Me)
	}

	// All routes below require valid JWT
	protected := admin.Group("")
	protected.Use(middleware.RequireInternalAuth(deps.JWT))

	// Application review — operators and supervisors
	reviewGroup := protected.Group("/applications")
	reviewGroup.Use(middleware.RequireRole("admin", "supervisor", "operator"))
	{
		reviewGroup.GET("", deps.Handlers.Admin.ListApplications)
		reviewGroup.GET("/:id", deps.Handlers.Admin.GetApplicationDetail)
		reviewGroup.GET("/:id/timeline", deps.Handlers.Admin.GetApplicationTimeline)

		// Operator (Maker) actions
		operatorGroup := reviewGroup.Group("")
		operatorGroup.Use(middleware.RequireRole("admin", "operator"))
		{
			operatorGroup.PATCH("/:id/open", deps.Handlers.Admin.OpenApplication)
			operatorGroup.PATCH("/:id/recommend", deps.Handlers.Admin.RecommendApplication)
			operatorGroup.POST("/:id/notes", deps.Handlers.Admin.AddNote)
		}

		// Supervisor (Checker) actions
		supervisorGroup := reviewGroup.Group("")
		supervisorGroup.Use(middleware.RequireRole("admin", "supervisor"))
		{
			supervisorGroup.PATCH("/:id/approve", deps.Handlers.Admin.ApproveApplication)
			supervisorGroup.PATCH("/:id/reject", deps.Handlers.Admin.RejectApplication)
		}

		// File serving untuk foto KTP dan selfie
		reviewGroup.GET("/:id/images/ktp", deps.Handlers.Admin.ServeKTPImage)
		reviewGroup.GET("/:id/images/selfie", deps.Handlers.Admin.ServeSelfieImage)
	}

	// User management — admin only
	userGroup := protected.Group("/users")
	userGroup.Use(middleware.RequireRole("admin"))
	{
		userGroup.GET("", deps.Handlers.Admin.ListUsers)
		userGroup.POST("", deps.Handlers.Admin.CreateUser)
		userGroup.GET("/:id", deps.Handlers.Admin.GetUser)
		userGroup.PATCH("/:id", deps.Handlers.Admin.UpdateUser)
		userGroup.PATCH("/:id/deactivate", deps.Handlers.Admin.DeactivateUser)
		userGroup.PATCH("/:id/reactivate", deps.Handlers.Admin.ReactivateUser)
	}

	// System configuration — admin only
	configGroup := protected.Group("/config")
	configGroup.Use(middleware.RequireRole("admin"))
	{
		configGroup.GET("", deps.Handlers.Admin.ListConfig)
		configGroup.PATCH("/:key", deps.Handlers.Admin.UpdateConfig)
	}

	// Audit logs — admin and supervisor can view
	auditGroup := protected.Group("/audit-logs")
	auditGroup.Use(middleware.RequireRole("admin", "supervisor"))
	{
		auditGroup.GET("", deps.Handlers.Admin.ListAuditLogs)
	}

	// Dashboard stats — for the admin home screen
	protected.GET("/dashboard/stats", deps.Handlers.Admin.GetDashboardStats)
}

// setupWebhookRoutes configures VIDA webhook endpoints.
func setupWebhookRoutes(router *gin.Engine, deps Dependencies) {
	webhooks := router.Group("/webhooks")

	// VIDA will call this endpoint when a document is signed or expired
	// No JWT auth — instead, we verify the VIDA webhook HMAC signature
	webhooks.POST("/vida", deps.Handlers.Webhook.HandleVida)
}
