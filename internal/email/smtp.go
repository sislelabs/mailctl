package email

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Attachment struct {
	Filename string
	Data     []byte
	MIMEType string
}

type Message struct {
	From        string
	To          []string
	Subject     string
	Body        string
	Attachments []Attachment
}

type SMTPConfig struct {
	Host        string
	Port        int
	User        string
	Pass        string
	DefaultFrom string
}

func Send(cfg *SMTPConfig, msg *Message) error {
	from := msg.From
	if from == "" {
		from = cfg.DefaultFrom
	}
	if from == "" {
		return fmt.Errorf("no sender address: set smtp.default_from in config or provide From in message")
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("SMTP connect to %s failed: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP client error: %w", err)
	}
	defer c.Close()

	// STARTTLS
	tlsConfig := &tls.Config{ServerName: cfg.Host}
	if err := c.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}

	// Auth
	if cfg.User != "" && cfg.Pass != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth failed: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM failed: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("SMTP RCPT TO <%s> failed: %w", to, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA failed: %w", err)
	}

	body := buildMIME(from, msg)
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("SMTP write failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP close failed: %w", err)
	}

	return c.Quit()
}

func buildMIME(from string, msg *Message) string {
	boundary := fmt.Sprintf("----=_mailctl_%d", time.Now().UnixNano())

	var b strings.Builder

	// Headers
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(msg.To, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) == 0 {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("\r\n")
		b.WriteString(base64Wrap([]byte(msg.Body)))
		return b.String()
	}

	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")

	// Body part
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	b.WriteString(base64Wrap([]byte(msg.Body)))
	b.WriteString("\r\n")

	// Attachments
	for _, att := range msg.Attachments {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + att.MIMEType + "; name=\"" + att.Filename + "\"\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + att.Filename + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("\r\n")
		b.WriteString(base64Wrap(att.Data))
		b.WriteString("\r\n")
	}

	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

func base64Wrap(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	// Wrap at 76 chars per RFC 2045
	var wrapped strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		wrapped.WriteString(encoded[i:end])
		wrapped.WriteString("\r\n")
	}
	return wrapped.String()
}
