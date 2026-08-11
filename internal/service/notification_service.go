package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/ioh"
	"github.com/cappyHoding/ptdpn-eform-service/internal/model"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/email"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NotificationService kirim email ke nasabah dan catat ke notification_logs.
type NotificationService interface {
	SendSubmitConfirmation(ctx context.Context, app *model.Application) error
	SendRejectionNotice(ctx context.Context, app *model.Application, reason string) error
	SendApprovalNotice(ctx context.Context, app *model.Application) error
	SendESignLink(ctx context.Context, app *model.Application, signLink string, deadline time.Time) error
	SendPostSignSMS(ctx context.Context, app *model.Application) error
}

type notificationService struct {
	mailer    *email.Mailer
	smsClient *ioh.SMSClient
	notifRepo repository.NotificationRepository
	log       *logger.Logger
}

func NewNotificationService(
	mailer *email.Mailer,
	smsClient *ioh.SMSClient,
	notifRepo repository.NotificationRepository,
	log *logger.Logger,
) NotificationService {
	return &notificationService{
		mailer:    mailer,
		smsClient: smsClient,
		notifRepo: notifRepo,
		log:       log,
	}
}

// ── SendSubmitConfirmation ────────────────────────────────────────────────────

func (s *notificationService) SendSubmitConfirmation(
	ctx context.Context, app *model.Application,
) error {
	if app.Customer.Email == nil {
		return fmt.Errorf("customer email is empty")
	}
	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}
	productLabel := productLabel(string(app.ProductType))

	body, err := email.RenderHTML(email.TmplSubmitConfirm, email.SubmitConfirmData{
		CustomerName: customerName,
		ProductName:  productLabel,
		AppID:        app.ID[:8],
		Details:      buildEmailDetails(app),
		LogoURI:      s.mailer.LogoDataURI(),
	})
	if err != nil {
		return fmt.Errorf("render submit confirm: %w", err)
	}
	subject := fmt.Sprintf("Pengajuan %s Anda Telah Diterima - BPR Perdana", productLabel)
	return s.send(ctx, app.ID, *app.Customer.Email, "submission_confirm", subject, body)
}

// ── SendRejectionNotice ───────────────────────────────────────────────────────

func (s *notificationService) SendRejectionNotice(
	ctx context.Context, app *model.Application, reason string,
) error {
	if app.Customer.Email == nil {
		return fmt.Errorf("customer email is empty")
	}
	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}
	productLabel := productLabel(string(app.ProductType))

	body, err := email.RenderHTML(email.TmplRejection, email.RejectionData{
		CustomerName: customerName,
		ProductName:  productLabel,
		Reason:       reason,
		Details:      buildEmailDetails(app),
		LogoURI:      s.mailer.LogoDataURI(),
		AppID:        app.ID[:8],
	})
	if err != nil {
		return fmt.Errorf("render rejection: %w", err)
	}
	subject := "Pemberitahuan Hasil Pengajuan - BPR Perdana"
	return s.send(ctx, app.ID, *app.Customer.Email, "rejection_email", subject, body)
}

// ── SendApprovalNotice ────────────────────────────────────────────────────────

func (s *notificationService) SendApprovalNotice(
	ctx context.Context, app *model.Application,
) error {
	if app.Customer.Email == nil {
		return fmt.Errorf("customer email is empty")
	}
	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}
	productLabel := productLabel(string(app.ProductType))

	body, err := email.RenderHTML(email.TmplApproval, email.ApprovalData{
		CustomerName: customerName,
		ProductName:  productLabel,
		Details:      buildEmailDetails(app),
		LogoURI:      s.mailer.LogoDataURI(),
		AppID:        app.ID[:8],
	})

	if err != nil {
		return fmt.Errorf("render approval: %w", err)
	}

	subject := "Selamat! Pengajuan Anda Disetujui - BPR Perdana"
	return s.send(ctx, app.ID, *app.Customer.Email, "approval_email", subject, body)
}

func (s *notificationService) SendESignLink(
	ctx context.Context, app *model.Application,
	signLink string, deadline time.Time,
) error {
	if app.Customer.Email == nil {
		return fmt.Errorf("customer email is empty")
	}
	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}
	productLabel := productLabel(string(app.ProductType))

	body, err := email.RenderHTML(email.TmplESignLink, email.ESignLinkData{
		CustomerName: customerName,
		ProductName:  productLabel,
		SignLink:     signLink,
		Deadline:     deadline.Format("2 January 2006"),
		Details:      buildEmailDetails(app),
		LogoURI:      s.mailer.LogoDataURI(),
		AppID:        app.ID[:8],
	})
	if err != nil {
		return fmt.Errorf("render esign: %w", err)
	}
	subject := fmt.Sprintf("Kontrak %s Siap Ditandatangani - BPR Perdana", productLabel)
	return s.send(ctx, app.ID, *app.Customer.Email, "esign_link_email", subject, body)
}

// ── private helper ────────────────────────────────────────────────────────────

func (s *notificationService) send(
	ctx context.Context,
	appID, toEmail, template, subject, body string,
) error {
	logID := uuid.New().String()
	subjectPtr := subject
	now := time.Now()

	notifLog := &model.NotificationLog{
		ID:            logID,
		ApplicationID: appID,
		Channel:       "EMAIL",
		Recipient:     toEmail,
		Template:      template,
		Subject:       &subjectPtr,
		Status:        "PENDING",
	}

	// Simpan log dulu dengan status PENDING
	if err := s.notifRepo.Create(ctx, notifLog); err != nil {
		s.log.Warn("Failed to create notification log",
			zap.String("app_id", appID),
			zap.Error(err),
		)
		// Jangan block pengiriman jika log gagal
	}

	// Kirim email
	sendErr := s.mailer.Send(email.Message{
		To:      toEmail,
		Subject: subject,
		Body:    body,
	})

	// Update log status
	if sendErr != nil {
		s.log.Error("Email send failed",
			zap.String("app_id", appID),
			zap.String("to", toEmail),
			zap.String("template", template),
			zap.Error(sendErr),
		)
		errMsg := sendErr.Error()
		notifLog.Status = "FAILED"
		notifLog.ErrorMessage = &errMsg
	} else {
		s.log.Info("Email sent",
			zap.String("app_id", appID),
			zap.String("to", toEmail),
			zap.String("template", template),
		)
		notifLog.Status = "SENT"
		notifLog.SentAt = &now
	}

	_ = s.notifRepo.Update(ctx, notifLog)

	return sendErr
}

func buildEmailDetails(app *model.Application) []email.EmailDetail {
	details := []email.EmailDetail{}

	switch {
	case app.SavingDetail != nil:
		s := app.SavingDetail
		details = append(details,
			email.EmailDetail{"Nama Produk", s.ProductName},
			email.EmailDetail{"Setoran Awal", formatIDR(s.InitialDeposit)},
			email.EmailDetail{"Sumber Dana", s.SourceOfFunds},
			email.EmailDetail{"Tujuan Menabung", s.SavingPurpose},
		)

	case app.DepositDetail != nil:
		d := app.DepositDetail
		details = append(details,
			email.EmailDetail{"Nama Produk", d.ProductName},
			email.EmailDetail{"Nominal Penempatan", formatIDR(d.PlacementAmount)},
			email.EmailDetail{"Tenor", fmt.Sprintf("%d bulan", d.TenorMonths)},
			email.EmailDetail{"Jenis Rollover", d.RolloverType},
			email.EmailDetail{"Sumber Dana", d.SourceOfFunds},
		)

	case app.LoanDetail != nil:
		l := app.LoanDetail
		details = append(details,
			email.EmailDetail{"Nama Produk", l.ProductName},
			email.EmailDetail{"Plafon Kredit", formatIDR(l.RequestedAmount)},
			email.EmailDetail{"Tenor", fmt.Sprintf("%d bulan", l.TenorMonths)},
			email.EmailDetail{"Tujuan Pinjaman", l.LoanPurpose},
		)
	}

	// Tambah info rekening pencairan jika ada
	if app.DisbursementData != nil {
		d := app.DisbursementData
		details = append(details,
			email.EmailDetail{"Bank Pencairan", d.BankName},
			email.EmailDetail{"No. Rekening", d.AccountNumber},
			email.EmailDetail{"Atas Nama", d.AccountHolder},
		)
	}

	return details
}

// formatIDR memformat angka ke format Rupiah
func formatIDR(amount uint64) string {
	if amount == 0 {
		return "—"
	}
	// Format manual tanpa library eksternal
	str := fmt.Sprintf("%d", amount)
	result := ""
	for i, ch := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += "."
		}
		result += string(ch)
	}
	return "Rp " + result
}

// ── SendPostSignSMS ─────────────────────────────────────────────────────────

func (s *notificationService) SendPostSignSMS(ctx context.Context, app *model.Application) error {
	if s.smsClient == nil {
		s.log.Warn("SMS client not configured, skipping post-sign SMS", zap.String("app_id", app.ID))
		return nil
	}

	if app.Customer.PhoneNumber == nil || *app.Customer.PhoneNumber == "" {
		return fmt.Errorf("customer phone number is missing for app %s", app.ID)
	}

	customerName := "Nasabah"
	if app.Customer.FullName != nil {
		customerName = *app.Customer.FullName
	}

	message := fmt.Sprintf("Sertifikat Elektronik atas nama %s telah digunakan untuk menandatangani dokumen Formulir Layanan E-Form BPR Perdana secara elektronik.", customerName)
	
	// Simpan log ke DB dulu sebagai PENDING
	notifID := uuid.New().String()
	subj := "Pemberitahuan Penandatanganan Dokumen"
	logEntry := &model.NotificationLog{
		ID:            notifID,
		ApplicationID: app.ID,
		Channel:       "SMS",
		Recipient:     *app.Customer.PhoneNumber,
		Template:      "post_sign_sms",
		Subject:       &subj,
		Status:        "PENDING",
	}

	if err := s.notifRepo.Create(ctx, logEntry); err != nil {
		s.log.Error("Failed to create SMS log", zap.Error(err))
	}

	// Kirim SMS
	s.log.Info("Sending post-sign SMS", zap.String("app_id", app.ID), zap.String("phone", *app.Customer.PhoneNumber))
	result, err := s.smsClient.Send(*app.Customer.PhoneNumber, message, "POST_SIGN_"+app.ID)

	if err != nil {
		logEntry.Status = "FAILED"
		errStr := err.Error()
		logEntry.ErrorMessage = &errStr
		s.log.Error("Failed to send post-sign SMS", zap.Error(err))
	} else if !result.IsSuccess() {
		logEntry.Status = "FAILED"
		errMsg := fmt.Sprintf("ErrorCode: %s", result.ErrorCode)
		logEntry.ErrorMessage = &errMsg
		s.log.Error("SMS provider returned error", zap.String("error_code", result.ErrorCode))
	} else {
		logEntry.Status = "SENT"
		now := time.Now()
		logEntry.SentAt = &now
		logEntry.ProviderMessageID = &result.TransactionID
	}

	// Update log status
	_ = s.notifRepo.Update(ctx, logEntry)

	return err
}

func productLabel(productType string) string {
	switch productType {
	case "SAVING":
		return "Tabungan"
	case "DEPOSIT":
		return "Deposito"
	case "LOAN":
		return "Pinjaman"
	default:
		return productType
	}
}
