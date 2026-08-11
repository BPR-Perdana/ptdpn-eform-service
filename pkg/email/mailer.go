package email

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Mailer struct {
	host      string
	port      int
	username  string
	password  string
	fromName  string
	fromEmail string
	logoB64   string // logo BPR Perdana sebagai base64, dimuat saat startup
	logoMime  string // MIME type logo, misal "image/png"
}

func New(host string, port int, username, password, fromName, fromEmail string) *Mailer {
	return &Mailer{
		host:      host,
		port:      port,
		username:  username,
		password:  password,
		fromName:  fromName,
		fromEmail: fromEmail,
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

func (m *Mailer) Send(msg Message) error {
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	if m.port == 465 {
		return m.sendSSL(addr, msg)
	}
	return m.sendSTARTTLS(addr, msg)
}

func (m *Mailer) sendSTARTTLS(addr string, msg Message) error {
	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	return smtp.SendMail(addr, auth, m.fromEmail, []string{msg.To}, []byte(m.buildRaw(msg)))
}

func (m *Mailer) sendSSL(addr string, msg Message) error {
	tlsCfg := &tls.Config{ServerName: m.host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS dial failed: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("SMTP client failed: %w", err)
	}
	defer client.Close()

	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed: %w", err)
	}
	if err = client.Mail(m.fromEmail); err != nil {
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
	from := fmt.Sprintf("%s <%s>", m.fromName, m.fromEmail)
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
	if m.host == "" {
		return fmt.Errorf("SMTP_HOST not configured")
	}
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
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
