package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cappyHoding/ptdpn-eform-service/config"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/vida"
	"github.com/cappyHoding/ptdpn-eform-service/internal/model"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/crypto"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/storage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ─── Input / Output DTOs ──────────────────────────────────────────────────────

// AgreementInput captures Step 1 data (T&C acceptance).
type AgreementInput struct {
	IPAddress string
	UserAgent string
}

// AgreementOutput is returned after Step 1.
// The agreement token is a short-lived proof that the customer accepted T&C.
type AgreementOutput struct {
	AgreementToken string `json:"agreement_token"`
	AcceptedAt     int64  `json:"accepted_at"`
}

// CreateApplicationInput captures Step 2: product selection ONLY.
// Contact info (email, phone) is collected in Step 4 (personal info).
type CreateApplicationInput struct {
	// Agreement proof from Step 1
	AgreementToken string

	// Product selection
	ProductType string // SAVING | DEPOSIT | LOAN
	IPAddress   string
	UserAgent   string

	// Product-specific fields
	Saving  *SavingInput
	Deposit *DepositInput
	Loan    *LoanInput
}

type SavingInput struct {
	ProductName    string
	InitialDeposit uint64
	SourceOfFunds  string
	SavingPurpose  string
}

type DepositInput struct {
	ProductName       string
	PlacementAmount   uint64
	TenorMonths       uint8
	RolloverType      string
	SourceOfFunds     string
	InvestmentPurpose string
}

type LoanInput struct {
	ProductName     string
	RequestedAmount uint64
	TenorMonths     uint8
	LoanPurpose     string
	PaymentSource   string
	SourceOfFunds   string
}

// CreateApplicationOutput is returned after Step 2.
// The frontend stores session_token and sends it in every subsequent request.
type CreateApplicationOutput struct {
	ApplicationID string `json:"application_id"`
	SessionToken  string `json:"session_token"`
	ExpiresAt     int64  `json:"expires_at"`
	ProductType   string `json:"product_type"`
	CurrentStep   uint8  `json:"current_step"`
}

// PersonalInfoInput captures Step 4: contact info + additional PII not on KTP.
// Email and phone are collected here because we need KTP identity (Step 3)
// to be established before recording contact details.
type PersonalInfoInput struct {
	// Contact info
	Email       string
	PhoneNumber string
	PhoneWA     string

	// Additional PII
	MothersMaidenName string
	Occupation        string
	WorkDuration      string
	MonthlyIncome     uint64
	Education         string
	WorkAddress       string
}

type LivenessTokenOutput struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	ExpiresAt   int64  `json:"expires_at"`
	SigningKey  string `json:"signing_key"`
}

// OCRInput captures Step 3: KTP image dari frontend dalam bentuk base64.
// Frontend mengirim JSON body dengan field image_base64 dan filename.
type OCRInput struct {
	ImageBase64 string // base64 string dari frontend (boleh ada/tanpa data URI prefix)
	Filename    string // nama file asli untuk deteksi MIME type, e.g. "ktp.jpg"
}

// LivenessInput captures Step 5.
// Frontend mengirim selfie base64 setelah Web SDK selesai.
// Backend memverifikasi ke VIDA Fraud Mitigation API menggunakan
// data identitas dari hasil OCR Step 3.
type LivenessInput struct {
	SelfieBase64 string

	// Dari VIDA Web SDK — untuk liveness_status
	LivenessTransactionID string  // SDK transaction ID
	LivenessScore         float64 // anti-spoofing score dari SDK
	LivenessLiveImage     bool    // true jika live image
	LivenessCode          int     // SDK result code (1041 = below threshold, dll)
}

// DisbursementInput captures Step 6: external bank account.
type DisbursementInput struct {
	BankName      string
	BankCode      string
	AccountNumber string
	AccountHolder string
}

// ApplicationSummary is the full view of an application for Step 7 review.
type ApplicationSummary struct {
	Application *model.Application `json:"application"`
}

type CollateralItemInput struct {
	CollateralType  string // SHM | SHGB | BPKB | Deposito | Lainnya
	EstimatedValue  uint64 // nilai agunan dalam IDR
	OwnershipStatus string // Milik Sendiri | Milik Pasangan | dll
	Description     string // deskripsi agunan (opsional)
}

// CollateralInput wraps semua items agunan untuk satu step.
type CollateralInput struct {
	Items []CollateralItemInput
}

type TransferProofInput struct {
	ImageBase64 string
	Filename    string
}

// ─── Service Errors ───────────────────────────────────────────────────────────

var (
	ErrApplicationNotFound  = errors.New("application not found")
	ErrInvalidStep          = errors.New("cannot perform this action at the current step")
	ErrMissingRequiredData  = errors.New("required data for this product type is missing")
	ErrDisbursementRequired = errors.New("disbursement data is required for this product type")
	ErrStepNotComplete      = errors.New("previous steps must be completed first")
	ErrAlreadySubmitted     = errors.New("this application has already been submitted")
	ErrPhoneNotVerified     = errors.New("phone number not verified")
)

// ─── Service Interface ────────────────────────────────────────────────────────

// ApplicationService defines all operations for the customer-facing form flow.
type ApplicationService interface {
	// Step 1
	AcceptAgreement(ctx context.Context, input AgreementInput) (*AgreementOutput, error)

	// Step 2
	CreateApplication(ctx context.Context, input CreateApplicationInput) (*CreateApplicationOutput, error)
	SaveCollateral(ctx context.Context, appID string, input CollateralInput) error

	// Get current state (for resume)
	GetApplication(ctx context.Context, id string) (*model.Application, error)
	GetApplicationWithDetails(ctx context.Context, id string) (*model.Application, error)
	MarkPhoneVerified(ctx context.Context, appID string) error
	MarkEmailVerified(ctx context.Context, appID string) error

	// Step 3 - upload KTP image, call VIDA OCR, save result
	ProcessOCR(ctx context.Context, appID string, input OCRInput) (*vida.OCRData, error)

	// Step 4
	UpdatePersonalInfo(ctx context.Context, appID string, input PersonalInfoInput) error
	GetLivenessToken(ctx context.Context, appID string) (*LivenessTokenOutput, error)

	// Step 5 - Liveness result stored after VIDA Web SDK completes
	SaveLivenessResult(ctx context.Context, appID string, input LivenessInput) error

	// Step 6
	UpdateDisbursement(ctx context.Context, appID string, input DisbursementInput) error

	// Step 7
	Submit(ctx context.Context, appID string) error

	UploadPaymentProof(ctx context.Context, appID string, fileData []byte, filename string) error
}

// ─── Implementation ───────────────────────────────────────────────────────────

type applicationService struct {
	appRepo      repository.ApplicationRepository
	customerRepo repository.CustomerRepository
	auditRepo    repository.AuditRepository
	vida         *vida.Services
	storage      *storage.Manager
	notifSvc     NotificationService
	log          *logger.Logger
	cfg          *config.Config
}

// NewApplicationService creates a new ApplicationService.
func NewApplicationService(
	appRepo repository.ApplicationRepository,
	customerRepo repository.CustomerRepository,
	auditRepo repository.AuditRepository,
	vidaServices *vida.Services,
	storageManager *storage.Manager,
	notifSvc NotificationService,
	log *logger.Logger,
	cfg *config.Config,
) ApplicationService {
	return &applicationService{
		appRepo:      appRepo,
		customerRepo: customerRepo,
		auditRepo:    auditRepo,
		vida:         vidaServices,
		storage:      storageManager,
		notifSvc:     notifSvc, // ← INI YANG KURANG
		log:          log,
		cfg:          cfg,
	}
}

// AcceptAgreement handles Step 1: records that a customer accepted T&C.
// Returns a short-lived token proving agreement acceptance.
// This token is required when creating the application in Step 2.
func (s *applicationService) AcceptAgreement(ctx context.Context, input AgreementInput) (*AgreementOutput, error) {
	// Generate a short-lived agreement token (valid for 30 minutes)
	// This proves the customer saw and accepted the T&C before creating an application
	token, err := crypto.GenerateSecureToken(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate agreement token: %w", err)
	}

	s.log.Info("Agreement accepted",
		zap.String("ip", input.IPAddress),
	)

	return &AgreementOutput{
		AgreementToken: token,
		AcceptedAt:     time.Now().Unix(),
	}, nil
}

// CreateApplication handles Step 2: creates the core application record,
// product detail record, customer record, and issues a session token.
//
// BUSINESS RULES:
//  1. Agreement token must be present (proof of T&C acceptance)
//  2. Product type must be valid
//  3. Product-specific detail must be provided
//  4. Returns a session token for subsequent step authentication
func (s *applicationService) CreateApplication(ctx context.Context, input CreateApplicationInput) (*CreateApplicationOutput, error) {
	// Validate product type and required fields
	if err := validateProductInput(input); err != nil {
		return nil, err
	}

	now := time.Now()

	// ── Create Customer record ────────────────────────────────────────────────
	// At Step 2 we only know the product they want — no PII yet.
	// Email, phone, and KTP data are collected in Steps 3 and 4.
	customer := &model.Customer{
		ID: uuid.New().String(),
	}
	if err := s.customerRepo.Create(ctx, customer); err != nil {
		return nil, fmt.Errorf("failed to create customer: %w", err)
	}

	// ── Create Application record ─────────────────────────────────────────────
	agreementAt := now
	app := &model.Application{
		ID:                  uuid.New().String(),
		CustomerID:          customer.ID,
		ProductType:         model.ProductType(input.ProductType),
		Status:              model.StatusDraft,
		CurrentStep:         3, // Step 2 selesai, customer lanjut ke Step 3
		LastStepCompleted:   2, // Step 2 (create application) sudah selesai
		AgreementAccepted:   true,
		AgreementAcceptedAt: &agreementAt,
		AgreementIP:         strPtrIfNotEmpty(input.IPAddress),
		AgreementUserAgent:  strPtrIfNotEmpty(input.UserAgent),
	}
	if err := s.appRepo.Create(ctx, app); err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}

	// ── Create product-specific detail record ─────────────────────────────────
	if err := s.createProductDetail(ctx, app.ID, input); err != nil {
		return nil, err
	}

	// ── Generate customer session token ───────────────────────────────────────
	// TTL: 2 hours — customers have 2 hours to complete the form
	sessionTTL := 2 * time.Hour
	rawToken, err := crypto.GenerateSecureToken(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}

	expiresAt := now.Add(sessionTTL)
	session := &model.CustomerSession{
		ID:            uuid.New().String(),
		ApplicationID: app.ID,
		Token:         rawToken,
		TokenHash:     crypto.HashToken(rawToken),
		IPAddress:     strPtrIfNotEmpty(input.IPAddress),
		UserAgent:     strPtrIfNotEmpty(input.UserAgent),
		ExpiresAt:     expiresAt,
	}
	if err := s.appRepo.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// ── Write audit log ───────────────────────────────────────────────────────
	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		ActorID:     strPtrIfNotEmpty(customer.ID),
		Action:      "APP_CREATED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(app.ID),
		Description: strPtrIfNotEmpty(fmt.Sprintf("Application created for product: %s", input.ProductType)),
		IPAddress:   strPtrIfNotEmpty(input.IPAddress),
	})

	s.log.Info("Application created",
		zap.String("application_id", app.ID),
		zap.String("product_type", input.ProductType),
		zap.String("customer_id", customer.ID),
	)

	return &CreateApplicationOutput{
		ApplicationID: app.ID,
		SessionToken:  rawToken, // Raw token returned to client — hash stored in DB
		ExpiresAt:     expiresAt.Unix(),
		ProductType:   input.ProductType,
		CurrentStep:   3, // Step 2 selesai, lanjut ke Step 3 (OCR)
	}, nil
}

// GetApplication returns basic application info for step validation.
func (s *applicationService) GetApplication(ctx context.Context, id string) (*model.Application, error) {
	app, err := s.appRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return nil, ErrApplicationNotFound
		}
		return nil, err
	}
	return app, nil
}

// GetApplicationWithDetails returns the full application with all related records.
func (s *applicationService) GetApplicationWithDetails(ctx context.Context, id string) (*model.Application, error) {
	app, err := s.appRepo.FindByIDWithDetails(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return nil, ErrApplicationNotFound
		}
		return nil, err
	}
	return app, nil
}

// ProcessOCR handles Step 3:
//  1. Simpan file KTP ke local storage
//  2. Kirim ke VIDA OCR API
//  3. Simpan hasil OCR ke database
//  4. Update customer record dengan data KTP
//  5. Advance ke Step 4
func (s *applicationService) ProcessOCR(ctx context.Context, appID string, input OCRInput) (*vida.OCRData, error) {
	app, err := s.validateAppForStep(ctx, appID, 3)
	if err != nil {
		return nil, err
	}

	// Guard: pastikan vida services sudah di-inject via NewApplicationService
	if s.vida == nil {
		s.log.Error("VIDA services not initialized — check main.go NewApplicationService arguments",
			zap.String("app_id", appID),
		)
		return nil, fmt.Errorf("VIDA services not initialized")
	}

	// ── 1. Decode base64 dari frontend ──────────────────────────────────────
	// Frontend bisa kirim dengan atau tanpa data URI prefix:
	//   - Dengan prefix: "data:image/jpeg;base64,/9j/4AAQ..."
	//   - Tanpa prefix:  "/9j/4AAQ..."
	// Kita strip prefix jika ada, lalu decode ke bytes
	rawB64 := input.ImageBase64
	if idx := strings.Index(rawB64, ","); idx != -1 {
		rawB64 = rawB64[idx+1:] // hapus "data:image/jpeg;base64,"
	}
	imageBytes, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 image: %w", err)
	}

	// ── 2. Panggil VIDA OCR API ───────────────────────────────────────────────
	// OCR service akan re-encode ke base64 dengan format yang dibutuhkan VIDA
	ocrData, err := s.vida.OCR.ExtractKTP(ctx, bytes.NewReader(imageBytes), input.Filename)
	if err != nil {
		s.log.Error("VIDA OCR failed", zap.String("app_id", appID), zap.Error(err))
		return nil, fmt.Errorf("OCR processing failed: %w", err)
	}

	// ── 3. Simpan file KTP ke storage (setelah OCR sukses) ───────────────────
	ext := filepath.Ext(input.Filename)
	if ext == "" {
		ext = ".jpg"
	}
	ktpFilename := appID + ext
	ktpPath, err := s.storage.SaveFile(storage.FileTypeKTP, ktpFilename, bytes.NewReader(imageBytes))
	if err != nil {
		// Log tapi jangan gagalkan proses — data OCR sudah berhasil
		s.log.Error("Failed to save KTP image to storage", zap.Error(err))
		ktpPath = ""
	}
	s.log.Info("KTP image saved", zap.String("path", ktpPath), zap.String("app_id", appID))

	// ── 4. Simpan hasil OCR ke database ──────────────────────────────────────
	result := &model.OCRResult{
		ID:              uuid.New().String(),
		ApplicationID:   appID,
		VidaRequestID:   ocrData.TransactionID,
		KTPImagePath:    ktpPath,
		NIK:             strPtrIfNotEmpty(ocrData.NIK),
		FullName:        strPtrIfNotEmpty(ocrData.Name),                         // OCRData.Name ← json:"nama"
		BirthPlace:      strPtrIfNotEmpty(ocrData.BirthPlace),                   // OCRData.BirthPlace ← json:"tempat_lahir"
		BirthDate:       strPtrIfNotEmpty(formatDOBForFraud(ocrData.BirthDate)), // DD-MM-YYYY → YYYY-MM-DD untuk MySQL DATE
		Gender:          strPtrIfNotEmpty(ocrData.Gender),                       // OCRData.Gender ← json:"jenis_kelamin"
		Address:         strPtrIfNotEmpty(ocrData.Address),                      // OCRData.Address ← json:"alamat"
		Religion:        strPtrIfNotEmpty(ocrData.Religion),
		MaritalStatus:   strPtrIfNotEmpty(ocrData.MaritalStatus),
		Occupation:      strPtrIfNotEmpty(ocrData.Occupation),
		Nationality:     strPtrIfNotEmpty(ocrData.Nationality),
		ConfidenceScore: &ocrData.ConfidenceScore,
		RawResponse:     model.JSON{"raw": ocrData},
	}
	if err := s.appRepo.UpsertOCRResult(ctx, result); err != nil {
		return nil, fmt.Errorf("failed to save OCR result: %w", err)
	}

	// ── 5. Update customer record dengan data dari KTP ───────────────────────
	customer, err := s.customerRepo.FindByID(ctx, app.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("failed to find customer: %w", err)
	}
	customer.NIK = strPtrIfNotEmpty(ocrData.NIK)
	customer.FullName = strPtrIfNotEmpty(ocrData.Name)
	customer.CurrentAddress = strPtrIfNotEmpty(ocrData.Address)
	customer.Occupation = strPtrIfNotEmpty(ocrData.Occupation)
	if err := s.customerRepo.Update(ctx, customer); err != nil {
		return nil, fmt.Errorf("failed to update customer from OCR: %w", err)
	}

	// ── 6. Advance ke Step 4 ──────────────────────────────────────────────────
	if err := s.appRepo.UpdateStep(ctx, appID, 4, 3); err != nil {
		return nil, fmt.Errorf("failed to advance step: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "APP_STEP_SAVED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty(fmt.Sprintf("Step 3 completed: KTP OCR success (NIK: %s)", ocrData.NIK)),
	})

	s.log.Info("OCR completed", zap.String("app_id", appID), zap.String("nik", ocrData.NIK))
	return ocrData, nil
}

// UpdatePersonalInfo handles Step 4: saves additional PII not found on KTP.
func (s *applicationService) UpdatePersonalInfo(ctx context.Context, appID string, input PersonalInfoInput) error {
	app, err := s.validateAppForStep(ctx, appID, 4)
	if err != nil {
		return err
	}

	// Update customer with additional info
	customer, err := s.customerRepo.FindByID(ctx, app.CustomerID)
	if err != nil {
		return fmt.Errorf("failed to find customer: %w", err)
	}

	// Contact info
	customer.Email = strPtrIfNotEmpty(input.Email)
	customer.PhoneNumber = strPtrIfNotEmpty(input.PhoneNumber)
	customer.PhoneNumberWA = strPtrIfNotEmpty(input.PhoneWA)

	// Additional PII
	customer.MothersMaidenName = strPtrIfNotEmpty(input.MothersMaidenName)
	customer.Occupation = strPtrIfNotEmpty(input.Occupation)
	customer.WorkDuration = strPtrIfNotEmpty(input.WorkDuration)
	customer.Education = strPtrIfNotEmpty(input.Education)
	customer.WorkAddress = strPtrIfNotEmpty(input.WorkAddress)
	if input.MonthlyIncome > 0 {
		customer.MonthlyIncome = &input.MonthlyIncome
	}

	if err := s.customerRepo.Update(ctx, customer); err != nil {
		return fmt.Errorf("failed to update customer personal info: %w", err)
	}

	// Advance step: current=5 (liveness), last_completed=4
	if err := s.appRepo.UpdateStep(ctx, appID, 5, 4); err != nil {
		return fmt.Errorf("failed to advance step: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "APP_STEP_SAVED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty("Step 4 completed: additional personal info saved"),
	})

	return nil
}

// SaveLivenessResult handles Step 5:
//  1. Frontend mengirim selfie base64 setelah Web SDK selesai
//  2. Backend fetch data KTP dari hasil OCR (Step 3) + data kontak (Step 4)
//  3. Backend call VIDA Fraud Mitigation API (FULL_FRAUD_ASSESSMENT)
//     → face match selfie vs KTP + verifikasi NIK ke Dukcapil
//  4. Simpan hasil ke database, advance ke Step 6
func (s *applicationService) SaveLivenessResult(ctx context.Context, appID string, input LivenessInput) error {
	app, err := s.validateAppForStep(ctx, appID, 5)
	if err != nil {
		return err
	}

	appWithDetails, err := s.appRepo.FindByIDWithDetails(ctx, appID)
	if err != nil {
		return fmt.Errorf("failed to load application details: %w", err)
	}
	if appWithDetails.OCRResult == nil {
		return fmt.Errorf("%w: OCR (Step 3) must be completed before liveness", ErrStepNotComplete)
	}

	customer, err := s.customerRepo.FindByID(ctx, app.CustomerID)
	if err != nil {
		return fmt.Errorf("failed to load customer: %w", err)
	}

	sdkLivenessStatus := "PASSED"
	sdkLivenessScore := input.LivenessScore

	if !input.LivenessLiveImage || input.LivenessScore < 0.6 {
		sdkLivenessStatus = "FAILED"
	}
	// Jika mock mode atau tidak ada data SDK, default PASSED
	if input.LivenessTransactionID == "" && !s.cfg.Vida.MockFraud {
		sdkLivenessStatus = "PASSED" // fallback
	}

	// ── Call VIDA Fraud API atau gunakan mock ─────────────────────────────────
	var fraudData *vida.FraudData

	if s.cfg.Vida.MockFraud {
		// Mock mode — bypass VIDA, langsung return hasil positif
		// Diaktifkan via VIDA_FRAUD_MOCK=true di .env
		s.log.Info("VIDA Fraud mock active — skipping real API call",
			zap.String("app_id", appID),
		)
		score := 0.92
		fraudData = &vida.FraudData{
			FaceMatch:      true,
			FaceMatchScore: score,
			DukcapilMatch:  true,
			RiskLevel:      "LOW",
			Decision:       "ACCEPT",
		}
	} else {
		nik := strVal(appWithDetails.OCRResult.NIK)
		fullName := strVal(appWithDetails.OCRResult.FullName)
		rawDOB := strVal(appWithDetails.OCRResult.BirthDate)
		dob := formatDOBForFraud(rawDOB)
		mobile := formatMobile(strVal(customer.PhoneNumber))
		email := strVal(customer.Email)

		s.log.Info("Fraud API DOB debug",
			zap.String("raw_from_db", rawDOB),
			zap.String("after_format", dob),
		)

		fraudData, err = s.vida.Fraud.VerifyIdentity(ctx,
			input.SelfieBase64, nik, fullName, dob, mobile, email,
		)
		if err != nil {
			s.log.Error("VIDA fraud verification failed",
				zap.String("app_id", appID), zap.Error(err))
			return fmt.Errorf("identity verification failed: %w", err)
		}
	}

	// ── Simpan selfie ke storage ──────────────────────────────────────────────
	var selfiePath *string
	if input.SelfieBase64 != "" {
		rawB64 := input.SelfieBase64
		// Strip data URI prefix jika ada
		if idx := strings.Index(rawB64, ","); idx != -1 {
			rawB64 = rawB64[idx+1:]
		}
		selfieBytes, decErr := base64.StdEncoding.DecodeString(rawB64)
		if decErr != nil {
			s.log.Warn("Failed to decode selfie base64",
				zap.String("app_id", appID), zap.Error(decErr))
		} else {
			savedPath, saveErr := s.storage.SaveFile(
				storage.FileTypeSelfie,
				appID+".jpg",
				bytes.NewReader(selfieBytes),
			)
			if saveErr != nil {
				s.log.Warn("Failed to save selfie image",
					zap.String("app_id", appID), zap.Error(saveErr))
			} else {
				selfiePath = &savedPath
				s.log.Info("Selfie image saved",
					zap.String("path", savedPath),
					zap.String("app_id", appID))
			}
		}
	}

	// ── Simpan hasil ke database (sama seperti sebelumnya) ───────────────────
	faceMatchStatus := "NOT_MATCHED"
	if fraudData.FaceMatch {
		faceMatchStatus = "MATCHED"
	}

	// Fraud API transaction ID untuk polling status VIDA
	fraudTransactionID := fraudData.TransactionID

	// VidaRequestID di DB = fraud transaction ID (untuk polling)
	vidaRequestID := fraudTransactionID
	if vidaRequestID == "" {
		vidaRequestID = input.LivenessTransactionID // fallback ke SDK ID
	}
	if vidaRequestID == "" {
		vidaRequestID = appID // last resort
	}

	// ── 4. Tentukan fraud_status dan kyc_event_id ─────────────────────────────
	fraudStatus := "002"
	var kycEventID *string

	if s.cfg.Vida.MockFraud {
		fraudStatus = "003"
		kid := vidaRequestID
		kycEventID = &kid
	}

	result := &model.LivenessResult{
		ID:              uuid.New().String(),
		ApplicationID:   appID,
		VidaRequestID:   vidaRequestID, // fraud API transaction ID (untuk polling)
		FraudStatus:     fraudStatus,
		KYCEventID:      kycEventID,
		LivenessStatus:  sdkLivenessStatus, // ← dari SDK, bukan dari Fraud API
		LivenessScore:   &sdkLivenessScore, // ← dari SDK
		FaceMatchStatus: strPtrIfNotEmpty(faceMatchStatus),
		FaceMatchScore:  &fraudData.FaceMatchScore,
		SelfieImagePath: selfiePath,
		RawResponse: model.JSON{
			// Data dari SDK
			"sdk_transaction_id": input.LivenessTransactionID,
			"sdk_score":          input.LivenessScore,
			"sdk_live_image":     input.LivenessLiveImage,
			"sdk_code":           input.LivenessCode,
			// Data dari Fraud API
			"fraud_transaction_id": fraudTransactionID,
			"face_match":           fraudData.FaceMatch,
			"face_match_score":     fraudData.FaceMatchScore,
			"dukcapil_match":       fraudData.DukcapilMatch,
			"risk_level":           fraudData.RiskLevel,
			"decision":             fraudData.Decision,
			"mock":                 s.cfg.Vida.MockFraud,
		},
	}

	if err := s.appRepo.UpsertLivenessResult(ctx, result); err != nil {
		return fmt.Errorf("failed to save liveness result: %w", err)
	}

	if err := s.appRepo.UpdateStep(ctx, appID, 6, 5); err != nil {
		return fmt.Errorf("failed to advance step: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:  "customer",
		Action:     "APP_STEP_SAVED",
		EntityType: strPtrIfNotEmpty("application"),
		EntityID:   strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty(fmt.Sprintf(
			"Step 5 completed: identity verification %s (mock: %v)",
			fraudData.Decision, s.cfg.Vida.MockFraud,
		)),
	})

	return nil
}

// UpdateDisbursement handles Step 6: saves the customer's bank account.
// Required for LOAN and DEPOSIT, optional for SAVING.
func (s *applicationService) UpdateDisbursement(ctx context.Context, appID string, input DisbursementInput) error {
	_, err := s.validateAppForStep(ctx, appID, 6)
	if err != nil {
		return err
	}

	data := &model.DisbursementData{
		ID:            uuid.New().String(),
		ApplicationID: appID,
		BankName:      input.BankName,
		BankCode:      input.BankCode,
		AccountNumber: input.AccountNumber,
		AccountHolder: input.AccountHolder,
	}

	if err := s.appRepo.UpsertDisbursement(ctx, data); err != nil {
		return fmt.Errorf("failed to save disbursement data: %w", err)
	}

	// Advance step: current=7 (summary), last_completed=6
	if err := s.appRepo.UpdateStep(ctx, appID, 7, 6); err != nil {
		return fmt.Errorf("failed to advance step: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "APP_STEP_SAVED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty("Step 6 completed: disbursement bank account saved"),
	})

	return nil
}

// Submit handles Step 7: validates all steps are complete and marks the
// application as PENDING_REVIEW so operators can begin review.
//
// BUSINESS RULES:
//  1. Steps 1–5 must be completed (OCR and liveness are mandatory)
//  2. For LOAN and DEPOSIT, step 6 (disbursement) is also required
//  3. Status must be DRAFT — cannot re-submit an already submitted application
func (s *applicationService) Submit(ctx context.Context, appID string) error {
	app, err := s.appRepo.FindByIDWithDetails(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return ErrApplicationNotFound
		}
		return err
	}

	// Guard: cannot submit if already submitted
	if app.Status != model.StatusDraft {
		return ErrAlreadySubmitted
	}

	// Validate all required steps are done
	if app.OCRResult == nil {
		return fmt.Errorf("%w: KTP OCR (Step 3) must be completed", ErrStepNotComplete)
	}
	if app.LivenessResult == nil {
		return fmt.Errorf("%w: Liveness check (Step 5) must be completed", ErrStepNotComplete)
	}

	// Disbursement required for LOAN and DEPOSIT
	if app.ProductType == model.ProductLoan || app.ProductType == model.ProductDeposit {
		if app.DisbursementData == nil {
			return ErrDisbursementRequired
		}
	}

	// All good — transition to PENDING_REVIEW
	now := time.Now()
	app.Status = model.StatusPendingReview
	app.SubmittedAt = &now
	app.CurrentStep = 8
	app.LastStepCompleted = 7

	if err := s.appRepo.Update(ctx, app); err != nil {
		return fmt.Errorf("failed to submit application: %w", err)
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("Email goroutine panicked", zap.Any("panic", r))
			}
		}()
		// app sudah di-load dengan FindByIDWithDetails sebelumnya
		// signature baru pakai *model.Application
		if err := s.notifSvc.SendSubmitConfirmation(context.Background(), app); err != nil {
			s.log.Warn("Submit confirmation email failed", zap.Error(err))
		}
	}()

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "APP_SUBMITTED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty("Application submitted for review"),
		NewValue:    model.JSON{"status": "PENDING_REVIEW"},
	})

	s.log.Info("Application submitted",
		zap.String("application_id", appID),
		zap.String("product_type", string(app.ProductType)),
	)

	return nil
}

// ─── Private Helpers ──────────────────────────────────────────────────────────

// validateAppForStep checks the application exists, is in DRAFT status,
// and has reached the minimum required step.
func (s *applicationService) validateAppForStep(ctx context.Context, appID string, step uint8) (*model.Application, error) {
	app, err := s.appRepo.FindByID(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return nil, ErrApplicationNotFound
		}
		return nil, err
	}

	// Can't modify a submitted or completed application
	if app.Status != model.StatusDraft {
		return nil, ErrAlreadySubmitted
	}

	// Customer must have reached this step (can't skip steps)
	// Allow current step OR re-doing a previous step
	if app.LastStepCompleted < step-1 {
		return nil, fmt.Errorf("%w: please complete Step %d first", ErrStepNotComplete, step-1)
	}

	return app, nil
}

// createProductDetail creates the appropriate product-specific record.
func (s *applicationService) createProductDetail(ctx context.Context, appID string, input CreateApplicationInput) error {
	switch model.ProductType(input.ProductType) {
	case model.ProductSaving:
		if input.Saving == nil {
			return fmt.Errorf("%w: saving details are required", ErrMissingRequiredData)
		}
		return s.appRepo.CreateSavingDetail(ctx, &model.SavingDetail{
			ApplicationID:  appID,
			ProductName:    input.Saving.ProductName,
			InitialDeposit: input.Saving.InitialDeposit,
			SourceOfFunds:  input.Saving.SourceOfFunds,
			SavingPurpose:  input.Saving.SavingPurpose,
		})

	case model.ProductDeposit:
		if input.Deposit == nil {
			return fmt.Errorf("%w: deposit details are required", ErrMissingRequiredData)
		}
		return s.appRepo.CreateDepositDetail(ctx, &model.DepositDetail{
			ApplicationID:     appID,
			ProductName:       input.Deposit.ProductName,
			PlacementAmount:   input.Deposit.PlacementAmount,
			TenorMonths:       input.Deposit.TenorMonths,
			RolloverType:      input.Deposit.RolloverType,
			SourceOfFunds:     input.Deposit.SourceOfFunds,
			InvestmentPurpose: strPtrIfNotEmpty(input.Deposit.InvestmentPurpose),
		})

	case model.ProductLoan:
		if input.Loan == nil {
			return fmt.Errorf("%w: loan details are required", ErrMissingRequiredData)
		}
		return s.appRepo.CreateLoanDetail(ctx, &model.LoanDetail{
			ApplicationID:   appID,
			ProductName:     input.Loan.ProductName,
			RequestedAmount: input.Loan.RequestedAmount,
			TenorMonths:     input.Loan.TenorMonths,
			LoanPurpose:     input.Loan.LoanPurpose,
			PaymentSource:   input.Loan.PaymentSource,
			SourceOfFunds:   input.Loan.SourceOfFunds,
		})

	default:
		return fmt.Errorf("%w: unknown product type: %s", ErrMissingRequiredData, input.ProductType)
	}
}

func (s *applicationService) SaveCollateral(ctx context.Context, appID string, input CollateralInput) error {
	_, err := s.validateAppForStep(ctx, appID, 3)
	if err != nil {
		return err
	}

	items := make([]*model.CollateralItem, 0, len(input.Items))
	for i, item := range input.Items {
		// ownership_status tidak ada kolom tersendiri — gabungkan ke description
		desc := item.Description
		if item.OwnershipStatus != "" {
			prefix := fmt.Sprintf("Kepemilikan: %s", item.OwnershipStatus)
			if desc != "" {
				desc = prefix + ". " + desc
			} else {
				desc = prefix
			}
		}

		ci := &model.CollateralItem{
			ID:             uuid.New().String(),
			ApplicationID:  appID,
			CollateralType: item.CollateralType,
			SortOrder:      uint8(i + 1),
		}
		if desc != "" {
			ci.Description = &desc
		}
		if item.EstimatedValue > 0 {
			ci.EstimatedValue = &item.EstimatedValue
		}
		items = append(items, ci)
	}

	if err := s.appRepo.UpsertCollateralItems(ctx, appID, items); err != nil {
		return fmt.Errorf("failed to save collateral: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "APP_STEP_SAVED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty(fmt.Sprintf("Step 3 completed: %d collateral item(s) saved", len(items))),
	})

	s.log.Info("Collateral saved",
		zap.String("app_id", appID),
		zap.Int("items", len(items)),
	)
	return nil
}

// validateProductInput checks that required product fields are present.
func validateProductInput(input CreateApplicationInput) error {
	switch input.ProductType {
	case "SAVING", "DEPOSIT", "LOAN":
		return nil
	default:
		return fmt.Errorf("%w: product_type must be SAVING, DEPOSIT, or LOAN", ErrMissingRequiredData)
	}
}

func (s *applicationService) writeAudit(ctx context.Context, entry *model.AuditLog) {
	entry.CreatedAt = time.Now()
	if err := s.auditRepo.Write(ctx, entry); err != nil {
		s.log.Error("Failed to write audit log",
			zap.String("action", entry.Action),
			zap.Error(err),
		)
	}
}

func (s *applicationService) GetLivenessToken(ctx context.Context, appID string) (*LivenessTokenOutput, error) {
	// Pastikan application ada
	app, err := s.appRepo.FindByIDWithDetails(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("load application failed: %w", err)
	}

	// ── Guard: nomor HP & Email harus sudah diverifikasi via OTP ─────────────────────
	// Verifikasi dilakukan di Step 4 (UpdatePersonalInfo + OTP).
	// Tanpa phone_verified & email_verified, nasabah tidak bisa lanjut ke Step 5 (liveness).
	if app.Customer.PhoneVerified == nil || !*app.Customer.PhoneVerified {
		return nil, fmt.Errorf("%w: selesaikan verifikasi OTP HP terlebih dahulu",
			ErrPhoneNotVerified)
	}
	if app.Customer.EmailVerified == nil || !*app.Customer.EmailVerified {
		return nil, fmt.Errorf("%w: selesaikan verifikasi OTP Email terlebih dahulu",
			ErrPhoneNotVerified)
	}

	// Ambil token dari VIDA OCR client (credential OCR dan Fraud sama)
	token, expiresAt, expiresIn, err := s.vida.OCR.Client().GetTokenWithCredentials(
		ctx,
		s.cfg.Vida.WebSDK.ClientID,
		s.cfg.Vida.WebSDK.SecretKey,
	)
	if err != nil {
		s.log.Error("Failed to get Web SDK token", // ← TAMBAHKAN INI
			zap.String("app_id", appID),
			zap.Error(err),
		)
		s.log.Info("WebSDK credentials check",
			zap.String("client_id", s.cfg.Vida.WebSDK.ClientID),
			zap.Bool("secret_empty", s.cfg.Vida.WebSDK.SecretKey == ""),
		)
		return nil, fmt.Errorf("failed to get Web SDK token: %w", err)
	}

	return &LivenessTokenOutput{
		AccessToken: token,
		SigningKey:  s.cfg.Vida.SigningKey,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
		ExpiresAt:   expiresAt,
	}, nil
}

func (s *applicationService) MarkPhoneVerified(ctx context.Context, appID string) error {
	return s.customerRepo.MarkPhoneVerified(ctx, appID)
}

func (s *applicationService) MarkEmailVerified(ctx context.Context, appID string) error {
	return s.customerRepo.MarkEmailVerified(ctx, appID)
}

// UploadPaymentProof menyimpan bukti transfer nasabah.
// Hanya berlaku untuk produk SAVING dan DEPOSIT.
// Tidak ada workflow validasi — hanya disimpan sebagai dokumentasi.
func (s *applicationService) UploadPaymentProof(
	ctx context.Context,
	appID string,
	fileData []byte,
	filename string,
) error {
	app, err := s.appRepo.FindByID(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return ErrApplicationNotFound
		}
		return err
	}

	// Hanya untuk SAVING dan DEPOSIT
	if app.ProductType != model.ProductSaving && app.ProductType != model.ProductDeposit {
		return fmt.Errorf("%w: upload bukti transfer hanya untuk produk Tabungan dan Deposito",
			ErrMissingRequiredData)
	}

	// Hanya bisa upload setelah status COMPLETED
	if app.Status != model.StatusCompleted {
		return fmt.Errorf("%w: pengajuan belum selesai ditandatangani",
			ErrStepNotComplete)
	}

	// Simpan file
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	saveName := appID + "_proof" + ext
	proofPath, err := s.storage.SaveFile(
		storage.FileTypePaymentProof,
		saveName,
		bytes.NewReader(fileData),
	)
	if err != nil {
		return fmt.Errorf("failed to save payment proof: %w", err)
	}

	// Update DB
	now := time.Now()
	if err := s.appRepo.UpdatePaymentProof(ctx, appID, proofPath, now); err != nil {
		return fmt.Errorf("failed to update payment proof: %w", err)
	}

	s.log.Info("Payment proof uploaded",
		zap.String("app_id", appID),
		zap.String("path", proofPath),
	)
	return nil
}

// openFileFromPath membuka file dari path untuk dibaca ulang.
func openFileFromPath(path string) (*os.File, error) {
	return os.Open(path)
}

// strVal safely dereferences a *string pointer.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// formatDOBForFraud memastikan tanggal lahir dalam format YYYY-MM-DD
// yang dibutuhkan VIDA Fraud Mitigation API.
//
// Menangani berbagai kemungkinan input:
//   - "02-01-1980"              → DD-MM-YYYY dari VIDA OCR       → "1980-01-02"
//   - "1980-01-02"              → YYYY-MM-DD dari MySQL DATE      → "1980-01-02" (sudah benar)
//   - "1980-01-02 00:00:00 +0000 UTC" → dari MySQL dengan parseTime → "1980-01-02"
//   - "1980-01-02T00:00:00Z"    → ISO 8601                        → "1980-01-02"
func formatDOBForFraud(ktpDate string) string {
	if len(ktpDate) < 10 {
		return ktpDate
	}

	// Case 1: DD-MM-YYYY (dari OCR langsung, sebelum disimpan ke DB)
	// Ciri: karakter ke-2 dan ke-5 adalah '-', dan bagian tahun ada di akhir
	if ktpDate[2] == '-' && ktpDate[5] == '-' {
		// "02-01-1980" → "1980-01-02"
		return ktpDate[6:10] + "-" + ktpDate[3:5] + "-" + ktpDate[0:2]
	}

	// Case 2: YYYY-MM-DD atau YYYY-MM-DDTxx:xx:xxZ (dari MySQL, sudah urutan benar)
	// Ambil hanya 10 karakter pertama
	if ktpDate[4] == '-' && len(ktpDate) >= 10 {
		return ktpDate[:10]
	}

	return ktpDate
}

// formatMobile memastikan nomor HP dalam format +62.
func formatMobile(phone string) string {
	if len(phone) == 0 {
		return ""
	}
	// Jika dimulai dengan 08, ubah ke +628
	if len(phone) > 1 && phone[0] == '0' {
		return "+62" + phone[1:]
	}
	// Jika belum ada +, tambahkan
	if phone[0] != '+' {
		return "+" + phone
	}
	return phone
}

func strPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
