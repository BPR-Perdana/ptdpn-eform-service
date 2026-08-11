package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/cappyHoding/ptdpn-eform-service/config"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/vida"
	"github.com/cappyHoding/ptdpn-eform-service/internal/model"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	contractpdf "github.com/cappyHoding/ptdpn-eform-service/pkg/pdf"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/storage"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ContractService orchestrates the full contract lifecycle after approval:
//
//	APPROVE → GeneratePDF → eMeterai → eSign → status: SIGNING
//	Webhook  → SaveSignedPDF → status: COMPLETED
type ContractService interface {
	// InitiateContract dipanggil oleh ApproveApplication setelah supervisor approve.
	InitiateContract(ctx context.Context, appID string, actorID string) error

	// CompleteContract dipanggil oleh webhook VIDA ketika customer selesai TTD.
	CompleteContract(ctx context.Context, vidaTransactionID string, signedPDFBase64 string) error

	// FailContract dipanggil webhook ketika sign expired/failed.
	FailContract(ctx context.Context, vidaTransactionID string, reason string) error

	// GetContractForAgreement mengembalikan data ringkasan kontrak untuk ditampilkan
	// di halaman agreement sebelum nasabah diarahkan ke link TTD VIDA.
	// Endpoint ini PUBLIC — tidak butuh session token.
	GetContractForAgreement(ctx context.Context, appID string) (*ContractAgreementData, error)

	// AcceptESignTOS menyimpan flag persetujuan TOS nasabah lalu mengembalikan
	// sign_link VIDA agar frontend bisa redirect.
	AcceptESignTOS(ctx context.Context, appID string, ip string) (string, error)
}

// ContractAgreementData adalah data yang dikembalikan untuk halaman agreement.
type ContractAgreementData struct {
	ApplicationID   string     `json:"application_id"`
	CustomerName    string     `json:"customer_name"`
	ProductType     string     `json:"product_type"`
	SignDeadline    *time.Time `json:"sign_deadline"`
	TOSAlreadyAccepted bool    `json:"tos_already_accepted"`
}

type contractService struct {
	appRepo      repository.ApplicationRepository
	contractRepo repository.ContractRepository
	auditRepo    repository.AuditRepository
	vida         *vida.Services
	storage      *storage.Manager
	logoPath     string              // path ke file logo perusahaan
	serverIP     string              // IP server untuk eSign requestInfo.srcIp
	notifSvc     NotificationService
	cfg          *config.Config
	log          *logger.Logger
}

func NewContractService(
	appRepo repository.ApplicationRepository,
	contractRepo repository.ContractRepository,
	auditRepo repository.AuditRepository,
	vidaServices *vida.Services,
	storageManager *storage.Manager,
	logoPath string,
	notifSvc NotificationService,
	cfg *config.Config,
	log *logger.Logger,
) ContractService {
	return &contractService{
		appRepo:      appRepo,
		contractRepo: contractRepo,
		auditRepo:    auditRepo,
		vida:         vidaServices,
		storage:      storageManager,
		logoPath:     logoPath,
		serverIP:     getOutboundIP(),
		notifSvc:     notifSvc,
		cfg:          cfg,
		log:          log,
	}
}

// getOutboundIP mendapatkan IP outbound server.
// Digunakan sebagai srcIp pada request eSign ke VIDA.
func getOutboundIP() string {
	// Dial UDP ke public DNS — tidak benar-benar konek, hanya untuk resolve IP lokal
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// ─── InitiateContract ─────────────────────────────────────────────────────────

func (s *contractService) InitiateContract(ctx context.Context, appID string, actorID string) error {
	// 1. Load application dengan semua data
	app, err := s.appRepo.FindByIDWithDetails(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return ErrApplicationNotFound
		}
		return fmt.Errorf("load application failed: %w", err)
	}

	// 2. Dapatkan email customer
	customerEmail := ""
	if app.Customer.Email != nil {
		customerEmail = *app.Customer.Email
	}
	if customerEmail == "" {
		return fmt.Errorf("customer email not found — cannot register eSign")
	}

	// ── Cek mock mode dulu sebelum panggil VIDA apapun ─────────────────────
	if s.cfg.Vida.MockContract {
		return s.initiateMockContract(ctx, app, appID, actorID, customerEmail)
	}

	// 3. Generate PDF kontrak
	s.log.Info("Generating contract PDF", zap.String("app_id", appID))
	result, err := contractpdf.GenerateContract(buildContractData(app, s.logoPath))
	if err != nil {
		return fmt.Errorf("PDF generation failed: %w", err)
	}

	s.log.Info("Contract PDF generated",
		zap.Int("total_pages", result.TotalPages),
		zap.Int("sig_page", result.SignaturePosition.PageIndex),
		zap.Int("sig_x", result.SignaturePosition.X),
		zap.Int("sig_y", result.SignaturePosition.Y),
	)

	pdfPath, err := s.storage.SaveFile(
		storage.FileTypeContract,
		appID+".pdf",
		bytes.NewReader(result.PDFBytes),
	)
	if err != nil {
		return fmt.Errorf("failed to save contract PDF: %w", err)
	}
	s.log.Info("Contract PDF saved", zap.String("path", pdfPath))

	// 4. Apply eMeterai (best-effort — tidak block jika gagal)
	// var materaiID *string
	// var materaiAppliedAt *time.Time

	// stampCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	// defer cancel()

	// stampResult, stampErr := s.vida.EMeterai.ApplyStamp(stampCtx, pdfPath, appID)
	// if stampErr != nil {
	// 	s.log.Warn("eMeterai failed, proceeding without stamp",
	// 		zap.String("app_id", appID), zap.Error(stampErr))
	// } else if stampResult != nil && stampResult.StampedDoc != "" {
	// 	stampedBytes, decErr := base64.StdEncoding.DecodeString(stampResult.StampedDoc)
	// 	if decErr == nil {
	// 		if stamppedPath, saveErr := s.storage.SaveFile(
	// 			storage.FileTypeContract, appID+".pdf",
	// 			bytes.NewReader(stampedBytes),
	// 		); saveErr == nil {
	// 			pdfPath = stamppedPath
	// 			now := time.Now()
	// 			materaiAppliedAt = &now
	// 			mid := stampResult.MateraiID
	// 			materaiID = &mid
	// 			s.log.Info("eMeterai applied", zap.String("serial", stampResult.SerialNumber))
	// 		}
	// 	}
	// }

	// 5. Register eSign
	if s.vida.DirectSign == nil {
		return fmt.Errorf("Direct Sign service not initialized — set VIDA_DSIGN credentials")
	}

	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}

	customerPhone := ""
	if app.Customer.PhoneNumber != nil {
		customerPhone = normalizePhone(*app.Customer.PhoneNumber)
	}
	_ = customerPhone
	sigPos := result.SignaturePosition

	sigResult, err := s.vida.DirectSign.CreateAndStartEnvelope(ctx, vida.CreateEnvelopeInput{
		PDFPath:        pdfPath,
		EnvelopeName:   fmt.Sprintf("Kontrak %s - %s", productLabel(string(app.ProductType)), customerName),
		RecipientName:  customerName,
		RecipientEmail: customerEmail,
		KYCEventID:     getKYCEventID(app), // dari liveness_results.vida_request_id
		ExpirationDays: 7,
		SignX:          sigPos.X,
		SignY:          sigPos.Y,
		SignWidth:      sigPos.Width,
		SignHeight:     sigPos.Height,
		SignPageIndex:  sigPos.PageIndex,
	})
	if err != nil {
		return fmt.Errorf("direct sign envelope failed: %w", err)
	}

	// 6. Simpan contract document ke DB
	fileSize := uint32(len(result.PDFBytes))
	now := time.Now()
	deadline := now.Add(7 * 24 * time.Hour)
	envelopeID := sigResult.EnvelopeID
	signingURL := sigResult.SignatureLink

	doc := &model.ContractDocument{
		ID:                uuid.New().String(),
		ApplicationID:     appID,
		DocumentType:      string(app.ProductType),
		FilePath:          pdfPath,
		FileSizeBytes:     &fileSize,
		VidaSignRequestID: &envelopeID, // ← envelope_id dari Direct Sign
		EMateraiID:        nil,
		EMateraiAppliedAt: nil,
		SignStatus:        "SIGNING",
		SignLink:          &signingURL, // ← link TTD untuk nasabah
		SignLinkSentAt:    &now,
		SignDeadline:      &deadline,
		GeneratedAt:       now,
	}
	if err := s.contractRepo.CreateContract(ctx, doc); err != nil {
		return fmt.Errorf("failed to save contract record: %w", err)
	}

	// 7. Update status → SIGNING
	if err := s.appRepo.UpdateStatus(ctx, appID, model.StatusSigning); err != nil {
		return fmt.Errorf("failed to update status to SIGNING: %w", err)
	}

	if s.notifSvc != nil {
		// Kirim link ke halaman agreement (bukan langsung ke VIDA)
		agreementURL := fmt.Sprintf("%s/esign-agreement?id=%s",
			s.cfg.App.FrontendURL, appID)
		if err := s.notifSvc.SendESignLink(context.Background(), app, agreementURL, deadline); err != nil {
			s.log.Warn("eSign email failed", zap.Error(err))
		}
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:  "internal_user",
		ActorID:    &actorID,
		Action:     "CONTRACT_INITIATED",
		EntityType: strPtrIfNotEmpty("application"),
		EntityID:   strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty(fmt.Sprintf(
			"Direct Sign envelope created, link sent to %s", customerEmail,
		)),
		NewValue: model.JSON{
			"status":      "SIGNING",
			"envelope_id": envelopeID,
		},
	})

	return nil
}

func (s *contractService) initiateMockContract(
	ctx context.Context,
	app *model.Application,
	appID, actorID, customerEmail string,
) error {
	now := time.Now()
	deadline := now.Add(7 * 24 * time.Hour)
	mockSignLink := fmt.Sprintf("https://sign.vida.id/mock/%s", appID[:8])

	doc := &model.ContractDocument{
		ID:             uuid.New().String(),
		ApplicationID:  appID,
		DocumentType:   string(app.ProductType),
		FilePath:       "mock/contract/" + appID + ".pdf",
		SignStatus:     "SIGNING",
		SignLink:       &mockSignLink,
		SignLinkSentAt: &now,
		SignDeadline:   &deadline,
		GeneratedAt:    now,
	}
	if err := s.contractRepo.CreateContract(ctx, doc); err != nil {
		return fmt.Errorf("failed to save mock contract: %w", err)
	}

	if err := s.appRepo.UpdateStatus(ctx, appID, model.StatusSigning); err != nil {
		return fmt.Errorf("failed to update status to SIGNING: %w", err)
	}

	if s.notifSvc != nil {
		agreementURL := fmt.Sprintf("%s/esign-agreement?id=%s",
			s.cfg.App.FrontendURL, appID)
		if err := s.notifSvc.SendESignLink(
			context.Background(), app, agreementURL, deadline,
		); err != nil {
			s.log.Warn("eSign email failed", zap.Error(err))
		}
	}

	s.log.Info("Mock contract initiated",
		zap.String("app_id", appID),
		zap.String("sign_link", mockSignLink),
	)

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "system",
		Action:      "CONTRACT_INITIATED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		Description: strPtrIfNotEmpty("Mock contract, eSign link sent to " + customerEmail),
		NewValue:    model.JSON{"status": "SIGNING", "mock": true},
	})

	return nil
}

// ─── CompleteContract ─────────────────────────────────────────────────────────

func (s *contractService) CompleteContract(ctx context.Context, vidaTransactionID string, signedPDFBase64 string) error {
	doc, err := s.contractRepo.FindContractBySignTrxID(ctx, vidaTransactionID)
	if err != nil {
		return fmt.Errorf("contract not found for envelope_id %s: %w", vidaTransactionID, err)
	}

	now := time.Now()

	// Simpan signed PDF jika ada (PoA mengirim via webhook, Direct Sign tidak)
	if signedPDFBase64 != "" {
		if signedBytes, decErr := base64.StdEncoding.DecodeString(signedPDFBase64); decErr == nil {
			signedPath, saveErr := s.storage.SaveFile(
				storage.FileTypeContract,
				doc.ApplicationID+"_signed.pdf",
				bytes.NewReader(signedBytes),
			)
			if saveErr == nil {
				doc.SignedFilePath = &signedPath
			}
		}
	}
	// Catatan: untuk Direct Sign, signed PDF bisa didownload via
	// DirectSign.DownloadSignedDocument() — bisa ditambahkan di sini nanti

	doc.SignStatus = "COMPLETED"
	doc.SignedAt = &now

	if err := s.contractRepo.UpdateContract(ctx, doc); err != nil {
		return fmt.Errorf("update contract failed: %w", err)
	}
	if err := s.appRepo.UpdateStatus(ctx, doc.ApplicationID, model.StatusCompleted); err != nil {
		return fmt.Errorf("update app status failed: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "CONTRACT_SIGNED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(doc.ApplicationID),
		Description: strPtrIfNotEmpty("Customer signed contract via VIDA Direct Sign"),
		NewValue:    model.JSON{"status": "COMPLETED", "envelope_id": vidaTransactionID},
	})

	// Fetch app detail to get phone and name
	if appDetail, errApp := s.appRepo.FindByIDWithDetails(ctx, doc.ApplicationID); errApp == nil {
		// Kirim post-sign SMS secara asynchronous agar tidak memblokir webhook
		go func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("goroutine panicked during post-sign SMS", zap.Any("panic", r))
				}
			}()
			bgCtx := context.Background()
			if err := s.notifSvc.SendPostSignSMS(bgCtx, appDetail); err != nil {
				s.log.Error("failed to send post-sign SMS", zap.Error(err), zap.String("app_id", doc.ApplicationID))
			}
		}()
	} else {
		s.log.Error("failed to fetch app details for post-sign SMS", zap.Error(errApp))
	}

	s.log.Info("Contract COMPLETED",
		zap.String("app_id", doc.ApplicationID),
		zap.String("envelope_id", vidaTransactionID),
	)
	return nil
}

// ─── FailContract ─────────────────────────────────────────────────────────────

func (s *contractService) FailContract(ctx context.Context, vidaTransactionID string, reason string) error {
	doc, err := s.contractRepo.FindContractBySignTrxID(ctx, vidaTransactionID)
	if err != nil {
		return fmt.Errorf("contract not found for trx %s: %w", vidaTransactionID, err)
	}

	doc.SignStatus = "EXPIRED"
	if err := s.contractRepo.UpdateContract(ctx, doc); err != nil {
		return fmt.Errorf("failed to update contract: %w", err)
	}
	// Rollback ke APPROVED — operator bisa trigger ulang
	if err := s.appRepo.UpdateStatus(ctx, doc.ApplicationID, model.StatusApproved); err != nil {
		return fmt.Errorf("failed to revert status: %w", err)
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "system",
		Action:      "CONTRACT_SIGN_FAILED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(doc.ApplicationID),
		Description: strPtrIfNotEmpty("eSign expired/failed: " + reason),
		NewValue:    model.JSON{"status": "APPROVED", "reason": reason},
	})
	return nil
}

// ─── GetContractForAgreement ─────────────────────────────────────────────────

func (s *contractService) GetContractForAgreement(ctx context.Context, appID string) (*ContractAgreementData, error) {
	app, err := s.appRepo.FindByIDWithDetails(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrApplicationNotFound) {
			return nil, ErrApplicationNotFound
		}
		return nil, fmt.Errorf("load application failed: %w", err)
	}

	// Hanya application berstatus SIGNING yang boleh diakses
	if app.Status != model.StatusSigning {
		return nil, ErrStepNotComplete
	}

	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}

	tosAccepted := false
	if app.ContractDocument != nil {
		tosAccepted = app.ContractDocument.ESignTOSAccepted
	}

	return &ContractAgreementData{
		ApplicationID:      appID,
		CustomerName:       customerName,
		ProductType:        string(app.ProductType),
		SignDeadline:       app.SignDeadline,
		TOSAlreadyAccepted: tosAccepted,
	}, nil
}

// ─── AcceptESignTOS ───────────────────────────────────────────────────────────

func (s *contractService) AcceptESignTOS(ctx context.Context, appID string, ip string) (string, error) {
	// 1. Pastikan contract ada dan app berstatus SIGNING
	doc, err := s.contractRepo.FindContractByAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", ErrApplicationNotFound
		}
		return "", fmt.Errorf("find contract failed: %w", err)
	}

	if doc.SignLink == nil || *doc.SignLink == "" {
		return "", fmt.Errorf("sign link belum tersedia")
	}

	signLink := *doc.SignLink

	// 2. Simpan flag TOS (idempotent — aman jika dipanggil berkali-kali)
	if err := s.contractRepo.AcceptESignTOS(ctx, appID, ip); err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return "", fmt.Errorf("save tos failed: %w", err)
		}
	}

	s.writeAudit(ctx, &model.AuditLog{
		ActorType:   "customer",
		Action:      "ESIGN_TOS_ACCEPTED",
		EntityType:  strPtrIfNotEmpty("application"),
		EntityID:    strPtrIfNotEmpty(appID),
		IPAddress:   strPtrIfNotEmpty(ip),
		Description: strPtrIfNotEmpty("Nasabah menyetujui syarat & ketentuan eSign VIDA"),
	})

	return signLink, nil
}

// ─── Private helpers ──────────────────────────────────────────────────────────

func (s *contractService) writeAudit(ctx context.Context, entry *model.AuditLog) {
	entry.CreatedAt = time.Now()
	if err := s.auditRepo.Write(ctx, entry); err != nil {
		s.log.Error("audit write failed", zap.String("action", entry.Action), zap.Error(err))
	}
}

// buildContractData maps model.Application → contractpdf.ContractData
func buildContractData(app *model.Application, logoPath string) contractpdf.ContractData {
	data := contractpdf.ContractData{
		ApplicationID: app.ID,
		ProductType:   string(app.ProductType),
		ApprovedAt:    time.Now(),
		LogoPath:      logoPath,
	}

	// Data dari OCR KTP
	if app.OCRResult != nil {
		data.FullName = derefStr(app.OCRResult.FullName)
		data.NIK = derefStr(app.OCRResult.NIK)
		data.BirthPlace = derefStr(app.OCRResult.BirthPlace)
		data.BirthDate = derefStr(app.OCRResult.BirthDate)
		data.Address = derefStr(app.OCRResult.Address)
		data.Kelurahan = derefStr(app.OCRResult.Kelurahan)
		data.Kecamatan = derefStr(app.OCRResult.Kecamatan)
		data.KabupatenKota = derefStr(app.OCRResult.KabupatenKota)
		data.Provinsi = derefStr(app.OCRResult.Provinsi)
		data.Occupation = derefStr(app.OCRResult.Occupation)
		data.Nationality = derefStr(app.OCRResult.Nationality)
	}

	// Data dari Customer (personal info diisi saat Step 4)
	data.Email = derefStr(app.Customer.Email)
	data.PhoneNumber = derefStr(app.Customer.PhoneNumber)
	data.Education = derefStr(app.Customer.Education)
	data.MonthlyIncome = derefUint64(app.Customer.MonthlyIncome)
	data.MothersMaidenName = derefStr(app.Customer.MothersMaidenName)

	// Detail produk
	switch app.ProductType {
	case model.ProductSaving:
		if app.SavingDetail != nil {
			data.Saving = &contractpdf.SavingData{
				ProductName:    app.SavingDetail.ProductName,
				InitialDeposit: app.SavingDetail.InitialDeposit,
				SourceOfFunds:  app.SavingDetail.SourceOfFunds,
				SavingPurpose:  app.SavingDetail.SavingPurpose,
			}
		}
	case model.ProductDeposit:
		if app.DepositDetail != nil {
			data.Deposit = &contractpdf.DepositData{
				ProductName:       app.DepositDetail.ProductName,
				PlacementAmount:   app.DepositDetail.PlacementAmount,
				TenorMonths:       app.DepositDetail.TenorMonths,
				RolloverType:      app.DepositDetail.RolloverType,
				SourceOfFunds:     app.DepositDetail.SourceOfFunds,
				InvestmentPurpose: derefStr(app.DepositDetail.InvestmentPurpose),
			}
		}
	case model.ProductLoan:
		if app.LoanDetail != nil {
			data.Loan = &contractpdf.LoanData{
				ProductName:     app.LoanDetail.ProductName,
				RequestedAmount: app.LoanDetail.RequestedAmount,
				TenorMonths:     app.LoanDetail.TenorMonths,
				LoanPurpose:     app.LoanDetail.LoanPurpose,
				PaymentSource:   app.LoanDetail.PaymentSource,
				SourceOfFunds:   app.LoanDetail.SourceOfFunds,
			}
		}
	}

	// Rekening pencairan
	if app.DisbursementData != nil {
		data.BankName = app.DisbursementData.BankName
		data.BankCode = app.DisbursementData.BankCode
		data.AccountNumber = app.DisbursementData.AccountNumber
		data.AccountHolder = app.DisbursementData.AccountHolder
	}

	return data
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Now()
	}
	return *t
}
func derefUint64(u *uint64) uint64 {
	if u == nil {
		return 0
	}
	return *u
}

func getKYCEventID(app *model.Application) string {
	if app.LivenessResult != nil && app.LivenessResult.KYCEventID != nil {
		return *app.LivenessResult.KYCEventID // ← dari fraud polling, bukan VidaRequestID
	}
	return ""
}

func normalizePhone(phone string) string {
	if strings.HasPrefix(phone, "0") {
		return "62" + phone[1:]
	}
	if strings.HasPrefix(phone, "+62") {
		return phone[1:]
	}
	return phone
}
