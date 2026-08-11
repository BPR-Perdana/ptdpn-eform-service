package main

import (
	"fmt"
	"os"

	"github.com/cappyHoding/ptdpn-eform-service/config"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/email"
)

func main() {
	targetEmail := ""
	if len(os.Args) > 1 {
		targetEmail = os.Args[1]
	}

	// Load configuration using the same loader as main.go
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	if targetEmail == "" {
		targetEmail = cfg.Email.FromEmail
		fmt.Printf("No target email provided, sending to yourself: %s\n", targetEmail)
	}

	mailer := email.New(email.Config{
		Provider:     cfg.Email.Provider,
		Host:         cfg.Email.Host,
		Port:         cfg.Email.Port,
		Username:     cfg.Email.Username,
		Password:     cfg.Email.Password,
		FromName:     cfg.Email.FromName,
		FromEmail:    cfg.Email.FromEmail,
		TenantID:     cfg.Email.GraphTenantID,
		ClientID:     cfg.Email.GraphClientID,
		ClientSecret: cfg.Email.GraphClientSecret,
	})

	msg := email.Message{
		To:      targetEmail,
		Subject: "Test Microsoft 365 E-Form BPR Perdana",
		Body:    "<h2>Halo!</h2><p>Ini adalah email percobaan untuk menguji integrasi Microsoft 365 (via Graph API/SMTP) pada aplikasi E-Form BPR Perdana.</p><p>Jika email ini diterima, berarti konfigurasi berhasil!</p>",
	}

	fmt.Printf("Sending test email to %s...\n", targetEmail)
	if cfg.Email.Provider == "graph" {
		fmt.Printf("Using Method: Microsoft Graph API\n")
		fmt.Printf("Tenant ID: %s\n", cfg.Email.GraphTenantID)
	} else {
		fmt.Printf("Using Method: SMTP\n")
		fmt.Printf("Using SMTP Server: %s:%d\n", cfg.Email.Host, cfg.Email.Port)
		fmt.Printf("Username: %s\n", cfg.Email.Username)
	}
	
	err = mailer.Send(msg)
	if err != nil {
		fmt.Printf("\n[FAILED] Gagal mengirim email:\n%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n[SUCCESS] Email berhasil dikirim ke %s!\n", targetEmail)
}
