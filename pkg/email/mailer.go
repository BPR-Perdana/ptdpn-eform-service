package email

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	Provider     string // "smtp" or "graph"
	Host         string
	Port         int
	Username     string
	Password     string
	FromName     string
	FromEmail    string
	TenantID     string
	ClientID     string
	ClientSecret string
}

type Mailer struct {
	cfg       Config
	logoB64   string // logo BPR Perdana sebagai base64, dimuat saat startup
	logoMime  string // MIME type logo, misal "image/png"
}

func New(cfg Config) *Mailer {
	return &Mailer{
		cfg: cfg,
	}
}


func (m *Mailer) LoadLogo(logoPath string) error {
	if logoPath == "" {
		return nil // tidak wajib — email tetap jalan tanpa logo
	}

	// Resolve ke absolute path jika masih relative
	absPath := logoPath
	if !filepath.IsAbs(logoPath) {
		// Coba dari working directory
		wd, err := os.Getwd()
		if err == nil {
			absPath = filepath.Join(wd, logoPath)
		}
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		// Coba path relative dari executable location sebagai fallback
		if exePath, exErr := os.Executable(); exErr == nil {
			exeDir := filepath.Dir(exePath)
			absPath = filepath.Join(exeDir, logoPath)
			data, err = os.ReadFile(absPath)
		}
		if err != nil {
			// Fallback 2: Coba runtime.Caller untuk development (go run)
			// Berguna di Windows jika go run dijalankan dari direktori berbeda
			_, filename, _, ok := runtime.Caller(0)
			if ok {
				// filename = .../pkg/email/mailer.go
				// projectRoot = .../
				projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
				absPath = filepath.Join(projectRoot, logoPath)
				data, err = os.ReadFile(absPath)
			}
		}
		if err != nil {
			return fmt.Errorf("logo file not found at %s: %w", logoPath, err)
		}
	}

	// Deteksi format — PNG atau JPG
	m.logoMime = "image/png"
	if strings.HasSuffix(strings.ToLower(absPath), ".jpg") ||
		strings.HasSuffix(strings.ToLower(absPath), ".jpeg") {
		m.logoMime = "image/jpeg"
	}

	m.logoB64 = base64.StdEncoding.EncodeToString(data)
	return nil
}

func (m *Mailer) LogoDataURI() string {
	if m.logoB64 == "" {
		return ""
	}
	return fmt.Sprintf("data:%s;base64,%s", m.logoMime, m.logoB64)
}

type Message struct {
	To      string
	Subject string
	Body    string
}

// ─── Custom LoginAuth untuk Microsoft 365 / Office 365 ──────────────────────
type loginAuth struct {
	username, password string
}

// LoginAuth is used for SMTP servers that only support AUTH LOGIN (like Microsoft 365).
func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username, password}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:":
			return []byte(a.username), nil
		case "Password:":
			return []byte(a.password), nil
		default:
			return nil, fmt.Errorf("unknown from server: %s", string(fromServer))
		}
	}
	return nil, nil
}

// ──────────────────────────────────────────────────────────────────────────────

func (m *Mailer) Send(msg Message) error {
	if m.cfg.Provider == "graph" {
		return m.sendGraphAPI(msg)
	}

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	if m.cfg.Port == 465 {
		return m.sendSSL(addr, msg)
	}
	return m.sendSTARTTLS(addr, msg)
}

func (m *Mailer) getAuth() smtp.Auth {
	// Microsoft 365 (Office 365) requires AUTH LOGIN.
	// Standard library's smtp.PlainAuth() uses AUTH PLAIN.
	if strings.Contains(strings.ToLower(m.cfg.Host), "office365.com") || 
	   strings.Contains(strings.ToLower(m.cfg.Host), "outlook.com") {
		return LoginAuth(m.cfg.Username, m.cfg.Password)
	}
	// Fallback to PLAIN auth for Mailtrap, Gmail, SendGrid, dll.
	return smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
}

func (m *Mailer) sendSTARTTLS(addr string, msg Message) error {
	auth := m.getAuth()
	return smtp.SendMail(addr, auth, m.cfg.FromEmail, []string{msg.To}, []byte(m.buildRaw(msg)))
}

func (m *Mailer) sendSSL(addr string, msg Message) error {
	tlsCfg := &tls.Config{ServerName: m.cfg.Host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS dial failed: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP client failed: %w", err)
	}
	defer client.Close()

	auth := m.getAuth()
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed: %w", err)
	}
	if err = client.Mail(m.cfg.FromEmail); err != nil {
		return fmt.Errorf("SMTP MAIL FROM failed: %w", err)
	}
	if err = client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("SMTP RCPT TO failed: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA failed: %w", err)
	}
	if _, err = fmt.Fprint(w, m.buildRaw(msg)); err != nil {
		return fmt.Errorf("SMTP write failed: %w", err)
	}
	return w.Close()
}

func (m *Mailer) buildRaw(msg Message) string {
	from := fmt.Sprintf("%s <%s>", m.cfg.FromName, m.cfg.FromEmail)
	headers := strings.Join([]string{
		"From: " + from,
		"To: " + msg.To,
		"Subject: " + msg.Subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}, "\r\n")
	return headers + "\r\n\r\n" + msg.Body
}

func (m *Mailer) TestConnection() error {
	if m.cfg.Provider == "graph" {
		_, err := m.getGraphAccessToken()
		if err != nil {
			return fmt.Errorf("Graph API test connection failed: %w", err)
		}
		return nil
	}
	
	if m.cfg.Host == "" {
		return fmt.Errorf("SMTP_HOST not configured")
	}
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("cannot reach SMTP server %s: %w", addr, err)
	}
	conn.Close()
	return nil
}

func RenderHTML(tmpl string, data any) (string, error) {
	t, err := template.New("email").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("template parse failed: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template render failed: %w", err)
	}
	return buf.String(), nil
}

// ─── Microsoft Graph API Implementation ───────────────────────────────────────

func (m *Mailer) getGraphAccessToken() (string, error) {
	if m.cfg.TenantID == "" || m.cfg.ClientID == "" || m.cfg.ClientSecret == "" {
		return "", fmt.Errorf("graph API credentials missing")
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", m.cfg.TenantID)
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", m.cfg.ClientID)
	data.Set("client_secret", m.cfg.ClientSecret)
	data.Set("scope", "https://graph.microsoft.com/.default")

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get graph token, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.AccessToken, nil
}

func (m *Mailer) sendGraphAPI(msg Message) error {
	token, err := m.getGraphAccessToken()
	if err != nil {
		return fmt.Errorf("graph API auth failed: %w", err)
	}

	type EmailAddress struct {
		Address string `json:"address"`
		Name    string `json:"name,omitempty"`
	}
	type Recipient struct {
		EmailAddress EmailAddress `json:"emailAddress"`
	}
	type ItemBody struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	}
	type Attachment struct {
		ODataType    string `json:"@odata.type"`
		Name         string `json:"name"`
		ContentType  string `json:"contentType"`
		ContentBytes string `json:"contentBytes"`
		IsInline     bool   `json:"isInline"`
		ContentID    string `json:"contentId,omitempty"`
	}
	type MessagePayload struct {
		Subject      string       `json:"subject"`
		Body         ItemBody     `json:"body"`
		ToRecipients []Recipient  `json:"toRecipients"`
		From         *Recipient   `json:"from,omitempty"`
		Attachments  []Attachment `json:"attachments,omitempty"`
	}

	payload := struct {
		Message         MessagePayload `json:"message"`
		SaveToSentItems string         `json:"saveToSentItems"`
	}{
		Message: MessagePayload{
			Subject: msg.Subject,
			Body: ItemBody{
				ContentType: "HTML",
				Content:     msg.Body,
			},
			ToRecipients: []Recipient{
				{EmailAddress: EmailAddress{Address: msg.To}},
			},
			From: &Recipient{
				EmailAddress: EmailAddress{
					Address: m.cfg.FromEmail,
					Name:    m.cfg.FromName,
				},
			},
		},
		SaveToSentItems: "false",
	}

	// Add logo inline attachment if it exists
	if m.logoB64 != "" {
		payload.Message.Attachments = []Attachment{
			{
				ODataType:    "#microsoft.graph.fileAttachment",
				Name:         "logo",
				ContentType:  m.logoMime,
				ContentBytes: m.logoB64,
				IsInline:     true,
				ContentID:    "logo",
			},
		}
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	sendURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/sendMail", m.cfg.FromEmail)
	req, err := http.NewRequest("POST", sendURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to send graph email, status: %d, body: %s", resp.StatusCode, string(body))
	}
	return nil
}
