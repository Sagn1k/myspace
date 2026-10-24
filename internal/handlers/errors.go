package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

// NotFound renders the 404 error page. It doubles as the catch-all handler.
func (h *Handler) NotFound(c *fiber.Ctx) error {
	return h.renderError(c, fiber.StatusNotFound)
}

// ErrorHandler is the Fiber error handler. It renders the error template with
// the error's status code and falls back to plain text if that fails too.
func (h *Handler) ErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
	}
	if code >= fiber.StatusInternalServerError {
		log.Printf("error: %s %s: %v", c.Method(), c.Path(), err)
	}

	if rerr := h.renderError(c, code); rerr != nil {
		log.Printf("error page: %v", rerr)
		c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)
		return c.Status(code).SendString(statusText(code))
	}
	return nil
}

func (h *Handler) renderError(c *fiber.Ctx, code int) error {
	heading, message := errorText(code)
	c.Status(code)
	return h.render(c, "error", fiber.Map{
		"Title":   heading,
		"Section": "",
		"NoIndex": true,
		"Code":    code,
		"Heading": heading,
		"Message": message,
		"Method":  c.Method(),
	})
}

func errorText(code int) (heading, message string) {
	switch {
	case code == fiber.StatusNotFound:
		return "Page not found", "The page you're looking for doesn't exist or has moved."
	case code >= fiber.StatusInternalServerError:
		return "Something went wrong", "An unexpected error occurred. Please try again in a moment."
	default:
		return statusText(code), "The request could not be completed."
	}
}

func statusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return text
	}
	return "Error"
}
