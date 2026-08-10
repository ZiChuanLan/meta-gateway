// Package webhook provides a throttled outbound notifier for operational
// events (channel auto-disabled / recovered / consecutive-failure thresholds)
// plus a multi-channel alert matrix (webhook / bark / serverchan / telegram /
// smtp) with content-signature cooldown. Notifications are best-effort and
// never block the request path.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind identifies the operational event being reported.
type Kind string

const (
	// ChannelDisabled fires when a channel is auto-disabled after reaching the
	// consecutive-failure threshold.
	ChannelDisabled Kind = "channel_disabled"
	// ChannelRecovered fires when a probe restores an auto-disabled channel.
	ChannelRecovered Kind = "channel_recovered"
)

// Notifier delivers throttled JSON notifications to a configured endpoint.
// A zero-throttle URL disables delivery entirely.
type Notifier struct {
	client   *http.Client
	mu       sync.Mutex
	url      string
	throttle time.Duration
	lastSent map[string]time.Time
	now      func() time.Time

	// Alert channels (multi-channel matrix).
	alert AlertConfig
	// alertSent coalesces alert signatures within the cooldown window.
	alertSent map[string]time.Time
}

// AlertConfig is the multi-channel alert matrix (Metapi-inspired). Empty
// fields disable their channel.
type AlertConfig struct {
	WebhookURL       string `json:"webhook_url,omitempty"`
	BarkURL          string `json:"bark_url,omitempty"`
	ServerChanKey    string `json:"serverchan_key,omitempty"`
	TelegramBotToken string `json:"telegram_bot_token,omitempty"`
	TelegramChatID   string `json:"telegram_chat_id,omitempty"`
	SMTPHost         string `json:"smtp_host,omitempty"`
	SMTPPort         int    `json:"smtp_port,omitempty"`
	SMTPUser         string `json:"smtp_user,omitempty"`
	SMTPPassword     string `json:"smtp_password,omitempty"`
	SMTPFrom         string `json:"smtp_from,omitempty"`
	SMTPTo           string `json:"smtp_to,omitempty"`
	CooldownSeconds  int    `json:"cooldown_seconds,omitempty"`
	// DailySummaryEnabled gates the scheduled daily digest.
	DailySummaryEnabled bool `json:"daily_summary_enabled,omitempty"`
}

// New creates a Notifier. url "" disables delivery; throttle <= 0 means every
// event is delivered (no coalescing).
func New(url string, throttle time.Duration) *Notifier {
	return &Notifier{
		client:    &http.Client{Timeout: 8 * time.Second},
		url:       url,
		throttle:  throttle,
		lastSent:  make(map[string]time.Time),
		alertSent: make(map[string]time.Time),
		now:       time.Now,
	}
}

// SetConfig hot-reloads the endpoint and throttle window.
func (n *Notifier) SetConfig(url string, throttle time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.url = url
	n.throttle = throttle
	if throttle <= 0 {
		// A disabled/zero throttle never coalesces; clear history.
		n.lastSent = make(map[string]time.Time)
	}
}

// Enabled reports whether delivery is configured.
func (n *Notifier) Enabled() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.url != ""
}

// SetAlertConfig hot-reloads the multi-channel alert matrix.
func (n *Notifier) SetAlertConfig(cfg AlertConfig) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alert = cfg
}

// AlertLevel is the alert severity.
type AlertLevel string

const (
	AlertInfo    AlertLevel = "info"
	AlertWarning AlertLevel = "warning"
	AlertError   AlertLevel = "error"
)

// SendAlert delivers a titled alert to every configured channel (webhook,
// bark, serverchan, telegram, smtp) with content-signature cooldown. Returns
// true when at least one channel accepted delivery. Failures are logged, never
// propagated.
func (n *Notifier) SendAlert(ctx context.Context, level AlertLevel, title, message string) bool {
	n.mu.Lock()
	cfg := n.alert
	cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
	sig := ""
	if cooldown > 0 {
		sig = strings.ToLower(string(level)) + "||" + title + "||" + message
		now := n.now()
		if last, ok := n.alertSent[sig]; ok && now.Sub(last) < cooldown {
			n.mu.Unlock()
			return false
		}
	}
	cfgURL := n.url // legacy single-webhook channel still applies
	n.mu.Unlock()

	delivered := &atomic.Bool{}
	var wg sync.WaitGroup
	send := func(name string, fn func() bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fn() {
				delivered.Store(true)
			}
		}()
	}

	if cfgURL != "" || cfg.WebhookURL != "" {
		send("webhook", func() bool {
			return n.deliverWebhook(ctx, cfgURL, cfg.WebhookURL, level, title, message)
		})
	}
	if cfg.BarkURL != "" {
		send("bark", func() bool {
			return n.deliverBark(ctx, cfg.BarkURL, title, message)
		})
	}
	if cfg.ServerChanKey != "" {
		send("serverchan", func() bool {
			return n.deliverServerChan(ctx, cfg.ServerChanKey, title, message)
		})
	}
	if cfg.TelegramBotToken != "" && cfg.TelegramChatID != "" {
		send("telegram", func() bool {
			return n.deliverTelegram(ctx, cfg.TelegramBotToken, cfg.TelegramChatID, title, message)
		})
	}
	if cfg.SMTPHost != "" && cfg.SMTPTo != "" {
		send("smtp", func() bool {
			return n.deliverSMTP(cfg, title, message)
		})
	}
	wg.Wait()
	// Only a delivery that actually reached a channel arms the cooldown; a
	// transient failure must not swallow subsequent alerts inside the window.
	if delivered.Load() && cooldown > 0 {
		n.mu.Lock()
		n.alertSent[sig] = n.now()
		n.mu.Unlock()
	}
	return delivered.Load()
}

func (n *Notifier) deliverWebhook(ctx context.Context, legacyURL, alertURL string, level AlertLevel, title, message string) bool {
	target := alertURL
	if target == "" {
		target = legacyURL
	}
	payload, err := json.Marshal(map[string]any{
		"level":     level,
		"title":     title,
		"message":   message,
		"timestamp": n.now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "meta-gateway-alert/1")
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("alert: webhook %s: %v", title, err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

func (n *Notifier) deliverBark(ctx context.Context, barkURL, title, message string) bool {
	target := strings.TrimRight(barkURL, "/") + "/" + url.PathEscape(title) + "/" + url.PathEscape(message)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("alert: bark %s: %v", title, err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

func (n *Notifier) deliverServerChan(ctx context.Context, key, title, message string) bool {
	form := url.Values{}
	form.Set("title", title)
	form.Set("desp", message)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://sctapi.ftqq.com/"+key+".send", strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("alert: serverchan %s: %v", title, err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

func (n *Notifier) deliverTelegram(ctx context.Context, botToken, chatID, title, message string) bool {
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    title + "\n" + message,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+botToken+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		log.Printf("alert: telegram %s: %v", title, err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

// smtpDialTimeout bounds the TCP connect; smtpSessionTimeout bounds the whole
// SMTP conversation (HELO/AUTH/DATA), which smtp.SendMail leaves unbounded and
// would otherwise block the synchronous alert path for minutes on a black-hole
// server.
const (
	smtpDialTimeout    = 10 * time.Second
	smtpSessionTimeout = 15 * time.Second
)

func (n *Notifier) deliverSMTP(cfg AlertConfig, title, message string) bool {
	port := cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(port))
	body := "From: " + cfg.SMTPFrom + "\r\n" +
		"To: " + cfg.SMTPTo + "\r\n" +
		"Subject: [meta-gateway] " + title + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		message
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpSessionTimeout))
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	defer client.Close()
	if cfg.SMTPUser != "" || cfg.SMTPPassword != "" {
		auth := smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
		if err := client.Auth(auth); err != nil {
			log.Printf("alert: smtp %s: %v", title, err)
			return false
		}
	}
	if err := client.Mail(cfg.SMTPFrom); err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	if err := client.Rcpt(cfg.SMTPTo); err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	writer, err := client.Data()
	if err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	if _, err := writer.Write([]byte(body)); err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	if err := writer.Close(); err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	if err := client.Quit(); err != nil {
		log.Printf("alert: smtp %s: %v", title, err)
		return false
	}
	return true
}

// Event is the JSON payload delivered to the endpoint.
type Event struct {
	Event       Kind   `json:"event"`
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Time        string `json:"time"`
}

// Notify sends one throttled event for the channel. It returns true when the
// event was actually delivered (or queued), false when it was coalesced by the
// throttle window or disabled. Delivery is asynchronous and failures are
// logged, never propagated.
func (n *Notifier) Notify(ctx context.Context, kind Kind, channelID int64, channelName, detail string) bool {
	n.mu.Lock()
	url := n.url
	throttle := n.throttle
	if url == "" {
		n.mu.Unlock()
		return false
	}
	key := string(kind) + fmt.Sprintf("|%d", channelID)
	now := n.now()
	if throttle > 0 {
		if last, ok := n.lastSent[key]; ok && now.Sub(last) < throttle {
			n.mu.Unlock()
			return false
		}
	}
	n.mu.Unlock()

	payload, err := json.Marshal(Event{
		Event:       kind,
		ChannelID:   channelID,
		ChannelName: channelName,
		Detail:      detail,
		Time:        now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("webhook: marshal event: %v", err)
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		log.Printf("webhook: new request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "meta-gateway-webhook/1")
	go func() {
		resp, err := n.client.Do(req)
		if err != nil {
			log.Printf("webhook: deliver %s channel=%d: %v", kind, channelID, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("webhook: deliver %s channel=%d: status %d", kind, channelID, resp.StatusCode)
			return
		}
		// Only a successful delivery arms the throttle window; a transient
		// failure must not swallow subsequent events for this channel.
		if throttle > 0 {
			n.mu.Lock()
			n.lastSent[key] = n.now()
			n.mu.Unlock()
		}
	}()
	return true
}
