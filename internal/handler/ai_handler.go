package handler

import (
	"flows/internal/dto"
	"flows/internal/service"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type AIHandler struct {
	AIService *service.AIService
}

func NewAIHandler(aiService *service.AIService) *AIHandler {
	return &AIHandler{AIService: aiService}
}

// GenerateFlow godoc
//
//	@Summary		Generate a Flow from Natural Language
//	@Description	Uses AI to convert a text description into a structured Flow definition, suggesting Connectors and identifying missing information.
//	@Tags			ai
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dto.GenerateFlowRequest	true	"Natural Language Prompt"
//	@Success		200		{object}	dto.GenerateFlowResponse
//	@Failure		500		{object}	map[string]string
//	@Router			/ai/generate [post]
func (h *AIHandler) GenerateFlow(c *gin.Context) {
	var req dto.GenerateFlowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.AIService.GenerateFlow(req.Prompt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *AIHandler) AnalyzeSignature(c *gin.Context) {
	var req dto.SignatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Payload invalido"})
		return
	}

	result, err := h.AIService.AnalyzeSignature(req.CapturedImage, req.ReferenceImage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *AIHandler) LivenessLuxand(c *gin.Context) {
	var req dto.LivenessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Payload invalido"})
		return
	}

	token := os.Getenv("LUXAND_TOKEN")
	result, err := h.AIService.LivenessLuxand(req.Image, token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
