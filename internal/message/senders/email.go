package senders

import (
	"context"
	"fmt"
	"net/smtp"
	"time"

	"github.com/zfd81/groot/internal/message"
)

// EmailSender sends events via SMTP
type EmailSender struct {
	host     string
	port     int
	username string
	password string
	from     string
}

// NewEmail creates a new email sender
func NewEmail(host string, port int, username, password, from string) *EmailSender {
	return &EmailSender{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
	}
}

// Name returns the sender name
func (s *EmailSender) Name() string {
	return "email"
}

// Send sends the event via SMTP email
func (s *EmailSender) Send(ctx context.Context, event message.Event) message.SendResult {
	msg := "From: " + s.from + "\r\n" +
		"To: " + s.from + "\r\n" +
		"Subject: [Groot] " + event.Title + "\r\n" +
		"\r\n" +
		event.Content + "\r\n"

	addr := fmt.Sprintf("%s:%d", s.host, s.port)

	auth := smtp.PlainAuth("", s.username, s.password, s.host)

	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(addr, auth, s.from, []string{s.from}, []byte(msg))
	}()

	select {
	case err := <-done:
		if err != nil {
			return message.SendResult{
				Channel:   "email",
				Success:   false,
				Message:   err.Error(),
				Timestamp: time.Now(),
			}
		}
		return message.SendResult{
			Channel:   "email",
			Success:   true,
			Message:   "发送成功",
			Timestamp: time.Now(),
		}
	case <-ctx.Done():
		return message.SendResult{
			Channel:   "email",
			Success:   false,
			Message:   ctx.Err().Error(),
			Timestamp: time.Now(),
		}
	}
}
