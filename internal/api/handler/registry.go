// Package handler contains all HTTP request handlers.
//
// A handler's only job is:
//  1. Parse and validate the HTTP request (body, params, headers)
//  2. Call the appropriate service method
//  3. Format and return the HTTP response
//
// Handlers should contain NO business logic. Business logic lives in services.
//
// Registry groups all handlers so they can be injected into the router cleanly.
package handler

// Registry holds all handler groups.
// When you add a new handler group (e.g., ProductHandler), add it here.
type Registry struct {
	Application *ApplicationHandler
	Auth        *AuthHandler
	Admin       *AdminHandler
	Webhook     *WebhookHandler
	Public      *PublicHandler
}
