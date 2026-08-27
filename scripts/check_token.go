package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"io"
	"encoding/base64"

	"github.com/cappyHoding/ptdpn-eform-service/config"
)

func decodeJWT(token string) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	fmt.Printf("Token Payload: %s\n", payload)
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", cfg.Email.GraphTenantID)
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", cfg.Email.GraphClientID)
	data.Set("client_secret", cfg.Email.GraphClientSecret)
	data.Set("scope", "https://graph.microsoft.com/.default")

	req, _ := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(body, &result)
	
	if result.AccessToken == "" {
		fmt.Println("No access token. Body:", string(body))
		return
	}

	fmt.Println("Got Token!")
	decodeJWT(result.AccessToken)
}
