package auth

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Middleware validates Authorization against configured API keys.
// Matches seerpy/CLI: raw key in Authorization header (no Bearer prefix required).
func Middleware(apiKeys []string) fiber.Handler {
	allowed := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		allowed[k] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		if len(allowed) == 0 {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "server has no SEER_API_KEYS configured",
			})
		}
		raw := strings.TrimSpace(c.Get("Authorization"))
		if raw == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing Authorization"})
		}
		key := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		if _, ok := allowed[key]; !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid api key"})
		}
		return c.Next()
	}
}
