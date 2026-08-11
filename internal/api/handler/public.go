package handler

import (
	"github.com/cappyHoding/ptdpn-eform-service/internal/repository"
	"github.com/cappyHoding/ptdpn-eform-service/pkg/response"
	"github.com/gin-gonic/gin"
)

type PublicHandler struct {
	configRepo repository.ConfigRepository
}

func NewPublicHandler(configRepo repository.ConfigRepository) *PublicHandler {
	return &PublicHandler{configRepo: configRepo}
}

// GetPublicConfig handles GET /api/v1/config/public
func (h *PublicHandler) GetPublicConfig(c *gin.Context) {
	configs, err := h.configRepo.FindPublic(c.Request.Context())
	if err != nil {
		response.InternalError(c, "")
		return
	}

	// We can map it into a simple key-value object for frontend convenience
	configMap := make(map[string]string)
	for _, cfg := range configs {
		configMap[cfg.ConfigKey] = cfg.ConfigValue
	}

	response.OK(c, "Public configuration retrieved", gin.H{"configs": configMap})
}
