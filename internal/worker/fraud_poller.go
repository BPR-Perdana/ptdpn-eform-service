package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/ioh"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/vida"
	"github.com/cappyHoding/ptdpn-eform-service/internal/model"
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/logger"
	"go.uber.org/zap"
)

// FraudPoller mengecek status fraud verification VIDA secara berkala.
// Interval: 30 menit (sesuai konfirmasi Abdi).
// Query: liveness_results WHERE fraud_status IN ('001','002').
type FraudPoller struct {
	appRepo  repository.ApplicationRepository
	vida     *vida.Services
	sms      *ioh.SMSClient
	interval time.Duration
	log      *logger.Logger
}

func NewFraudPoller(
	appRepo repository.ApplicationRepository,
	vida *vida.Services,
	sms *ioh.SMSClient,
	interval time.Duration,
	log *logger.Logger,
) *FraudPoller {
	return &FraudPoller{
		appRepo:  appRepo,
		vida:     vida,
		sms:      sms,
		interval: interval,
		log:      log,
	}
}

// Start menjalankan polling loop di background goroutine.
// Panggil di main.go dengan: go fraudPoller.Start(ctx)
func (p *FraudPoller) Start(ctx context.Context) {
	p.log.Info("Fraud poller started", zap.Duration("interval", p.interval))

	// Run sekali saat startup
	p.poll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.log.Info("Fraud poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *FraudPoller) poll(ctx context.Context) {
	// Cari semua aplikasi yang fraud statusnya masih pending
	apps, err := p.appRepo.FindPendingFraudApps(ctx)
	if err != nil {
		p.log.Error("Fraud poller FindPendingFraudApps failed", zap.Error(err))
		return
	}
	if len(apps) == 0 {
		return
	}

	p.log.Info("Fraud poller checking", zap.Int("count", len(apps)))

	for _, app := range apps {
		if app.LivenessResult == nil || app.LivenessResult.VidaRequestID == "" {
			continue
		}

		if app.LivenessResult.VidaRequestID == app.ID {
			p.log.Debug("Skipping — mock transaction ID",
				zap.String("app_id", app.ID))
			continue
		}

		if err := p.checkOne(ctx, &app); err != nil {
			p.log.Error("Fraud check failed",
				zap.String("app_id", app.ID),
				zap.Error(err),
			)
		}
		// Jeda 1 detik antar request agar tidak spam VIDA
		time.Sleep(1 * time.Second)
	}
}

func (p *FraudPoller) checkOne(ctx context.Context, app *model.Application) error {
	transactionID := app.LivenessResult.VidaRequestID

	if transactionID == app.ID {
		p.log.Debug("Skipping fraud poll — VidaRequestID is appID (mock/fallback)",
			zap.String("app_id", app.ID),
		)
		return nil
	}

	statusResult, err := p.vida.Fraud.GetFraudStatus(ctx, transactionID)
	if err != nil {
		return fmt.Errorf("GetFraudStatus failed: %w", err)
	}

	p.log.Info("Fraud status polled",
		zap.String("app_id", app.ID),
		zap.String("transaction_id", transactionID),
		zap.String("status", statusResult.Status),
	)

	switch statusResult.Status {
	case "001", "002":
		// Masih proses / menunggu manual review — skip
		return nil

	case "003", "007":
		if err := p.appRepo.UpdateLivenessFraudStatus(
			ctx, app.ID, statusResult.Status, statusResult.KYCEventID,
		); err != nil {
			return fmt.Errorf("UpdateLivenessFraudStatus failed: %w", err)
		}

		// ── Kirim SMS notifikasi sertifikat elektronik terbit ─────────────────
		if p.sms != nil {
			go p.sendCertificateSMS(ctx, app)
		}

		p.log.Info("Fraud APPROVED — kyc_event_id saved",
			zap.String("app_id", app.ID),
			zap.String("kyc_event_id", statusResult.KYCEventID),
		)

	case "004", "006":
		// Rejected atau certificate not issued
		if err := p.appRepo.UpdateLivenessFraudStatus(
			ctx, app.ID, statusResult.Status, "",
		); err != nil {
			return fmt.Errorf("UpdateLivenessFraudStatus failed: %w", err)
		}
		// Update status aplikasi ke FRAUD_REJECTED
		if err := p.appRepo.UpdateStatus(ctx, app.ID, model.StatusFraudRejected); err != nil {
			return fmt.Errorf("UpdateStatus FRAUD_REJECTED failed: %w", err)
		}
		p.log.Warn("Fraud REJECTED",
			zap.String("app_id", app.ID),
			zap.String("status", statusResult.Status),
		)
	}

	return nil
}

func (p *FraudPoller) sendCertificateSMS(ctx context.Context, app *model.Application) {
	if app.Customer.PhoneNumber == nil || *app.Customer.PhoneNumber == "" {
		p.log.Warn("Cannot send certificate SMS — phone number empty",
			zap.String("app_id", app.ID))
		return
	}

	phone := ioh.NormalizePhone(*app.Customer.PhoneNumber)
	refID := app.ID[:8]

	message := fmt.Sprintf(
		"Sertifikat Elektronik (SE) Anda telah diterbitkan oleh VIDA sebagai PSrE mitra dari PT BPR Daya Perdana Nusantara. Anda dapat mengakses SE Anda di https://sign.vida.id",
	)

	result, err := p.sms.Send(phone, message, "CERT-"+refID)
	if err != nil {
		p.log.Error("Certificate SMS failed",
			zap.String("app_id", app.ID),
			zap.String("phone", phone),
			zap.Error(err),
		)
		return
	}

	p.log.Info("Certificate SMS sent",
		zap.String("app_id", app.ID),
		zap.String("ioh_trx", result.TransactionID),
	)
}
