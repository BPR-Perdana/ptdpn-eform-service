package main

import (
	"fmt"
	"log"
	"github.com/spf13/viper"
	"github.com/cappyHoding/ptdpn-eform-service/internal/integration/ioh"
)

func main() {
	viper.SetConfigFile(".env")
	if err := viper.ReadInConfig(); err != nil {
		log.Println("Warning: Error loading .env file, relying on environment variables")
	}

	username := viper.GetString("IOH_SMS_USERNAME")
	password := viper.GetString("IOH_SMS_PASSWORD")
	senderID := viper.GetString("IOH_SMS_SENDER_ID")
	
	if username == "" || password == "" {
		log.Fatal("IOH_SMS_USERNAME or IOH_SMS_PASSWORD not set in .env")
	}

	client := ioh.NewSMSClient(username, password, senderID)
	
	phone := "6282267414035"
	message := "Sertifikat Elektronik atas nama Abdi Elman D A telah digunakan untuk menandatangani dokumen Formulir Layanan E-Form BPR Perdana secara elektronik."
	
	fmt.Printf("Sending SMS to %s...\n", phone)
	result, err := client.Send(phone, message, "TEST-VIDA-COMPLIANCE")
	
	if err != nil {
		log.Fatalf("Failed to send SMS: %v", err)
	}
	
	fmt.Printf("Success! Transaction ID: %s, Balance: %s, ErrorCode: %s\n", result.TransactionID, result.Balance, result.ErrorCode)
}
