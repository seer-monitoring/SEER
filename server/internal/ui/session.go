package ui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	sessionCookie = "seer_ui_session"
	sessionMaxAge = 7 * 24 * time.Hour
)

// Session signs and validates UI login cookies.
type Session struct {
	Secret  string
	APIKeys []string
}

func NewSession(secret string, apiKeys []string) *Session {
	if secret == "" {
		secret = deriveSecret(apiKeys)
	}
	return &Session{Secret: secret, APIKeys: apiKeys}
}

func deriveSecret(apiKeys []string) string {
	h := sha256.New()
	h.Write([]byte("seer-ui-default-secret:"))
	for _, k := range apiKeys {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func keyFingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func (s *Session) Sign(apiKey string) string {
	fp := keyFingerprint(apiKey)
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write([]byte(fp))
	return fp + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Session) Valid(token string) bool {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	fp, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write([]byte(fp))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return false
	}
	for _, k := range s.APIKeys {
		if keyFingerprint(k) == fp {
			return true
		}
	}
	return false
}

func (s *Session) ValidAPIKey(apiKey string) bool {
	apiKey = strings.TrimSpace(apiKey)
	for _, k := range s.APIKeys {
		if k == apiKey {
			return true
		}
	}
	return false
}

func (s *Session) SetCookie(c *fiber.Ctx, apiKey string) {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookie,
		Value:    s.Sign(apiKey),
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int(sessionMaxAge.Seconds()),
		Expires:  time.Now().Add(sessionMaxAge),
	})
}

func (s *Session) ClearCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   -1,
		Expires:  time.Now().Add(-time.Hour),
	})
}

func (s *Session) TokenFromRequest(c *fiber.Ctx) string {
	return strings.TrimSpace(c.Cookies(sessionCookie))
}

func (s *Session) Authenticated(c *fiber.Ctx) bool {
	return s.Valid(s.TokenFromRequest(c))
}

// RequireAuth redirects HTML navigations to login; JSON APIs get 401.
func (s *Session) RequireAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if s.Authenticated(c) {
			return c.Next()
		}
		accept := c.Get("Accept")
		path := c.Path()
		if strings.HasPrefix(path, "/api/") || strings.Contains(accept, "application/json") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		next := url.QueryEscape(c.OriginalURL())
		return c.Redirect("/ui/login?next="+next, fiber.StatusFound)
	}
}
