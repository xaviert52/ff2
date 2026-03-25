package dto

import "flows/internal/domain"

type GenerateFlowRequest struct {
	Prompt string `json:"prompt" binding:"required"`
}

type GenerateFlowResponse struct {
	Flow        *domain.Flow       `json:"flow,omitempty"`
	Connectors  []domain.Connector `json:"connectors,omitempty"`
	MissingInfo []string           `json:"missing_info,omitempty"`
	Explanation string             `json:"explanation,omitempty"`
}

// NUEVAS ESTRUCTURAS DE IA
type SignatureRequest struct {
	CapturedImage  string `json:"capturedImage"`
	ReferenceImage string `json:"referenceImage"`
}

type LivenessRequest struct {
	Image string `json:"image"`
}
