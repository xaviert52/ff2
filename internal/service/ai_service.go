package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flows/internal/domain"
	"flows/internal/dto"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/sashabaranov/go-openai"
)

type AIService struct {
	Client *openai.Client
}

func NewAIService(apiKey string) *AIService {
	return &AIService{
		Client: openai.NewClient(apiKey),
	}
}

func (s *AIService) GenerateFlow(prompt string) (*dto.GenerateFlowResponse, error) {
	systemPrompt := `You are an expert Flow Engineer for a workflow automation system.
Your task is to convert natural language descriptions into a JSON structure representing a Flow and optionally Connectors.

The system has the following Domain Models:

1. Flow Definition (JSON):
{
  "start_step": "step_id_1",
  "steps": {
    "step_id_1": {
      "id": "step_id_1",
      "type": "FORM" | "ACTION" | "DECISION",
      "description": "...",
      "next_step": "step_id_2",
      "schema": { ... JSON Schema for FORM input ... },
      "connector_id": 123 (integer, placeholder if new),
      "config": { ... key-value pairs ... }
    }
  }
}

2. Connector (if needed):
{
  "name": "...",
  "type": "REST" | "SOAP",
  "base_url": "...",
  "auth_type": "NONE" | "API_KEY" | "OAUTH2"
}

Output Format (JSON Only):
{
  "flow": {
     "name": "...",
     "description": "...",
     "definition": { ... FlowDefinition object ... }
  },
  "connectors": [ ... list of connectors to create ... ],
  "missing_info": [ ... list of questions if info is missing ... ],
  "explanation": "Brief explanation of what was generated"
}

Rules:
- If the user mentions an external API (e.g. "send email"), create a Connector definition for it.
- If the user implies a form (e.g. "ask for name"), create a FORM step with a JSON Schema.
- Use logical IDs for steps (e.g., "ask_name", "send_email").
- If critical info is missing (e.g., "what is the API URL?"), add it to "missing_info".
- Return valid JSON only.`

	resp, err := s.Client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{
				Type: openai.ChatCompletionResponseFormatTypeJSONObject,
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("AI generation failed: %w", err)
	}

	var result struct {
		Flow        *domain.Flow       `json:"flow"`
		Connectors  []domain.Connector `json:"connectors"`
		MissingInfo []string           `json:"missing_info"`
		Explanation string             `json:"explanation"`
	}

	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return &dto.GenerateFlowResponse{
		Flow:        result.Flow,
		Connectors:  result.Connectors,
		MissingInfo: result.MissingInfo,
		Explanation: result.Explanation,
	}, nil
}

func (s *AIService) AnalyzeSignature(capturedImage, referenceImage string) (map[string]interface{}, error) {
	systemPrompt := `Eres un experto forense analista de firmas. Compara las dos firmas proporcionadas y devuelve el analisis en formato JSON estricto:
{
  "similarityScore": numero (0-100),
  "pressureMatch": numero,
  "inclinationMatch": numero,
  "proportionsMatch": numero,
  "strokesMatch": numero,
  "overallStatus": "verified" o "not-verified",
  "explanation": "string"
}`
	resp, err := s.Client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: openai.GPT4o,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
				{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{Type: openai.ChatMessagePartTypeText, Text: "Firma capturada:"},
						{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: capturedImage}},
						{Type: openai.ChatMessagePartTypeText, Text: "Firma de referencia:"},
						{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: referenceImage}},
					},
				},
			},
			ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject},
			MaxTokens:      500,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("Fallo en OpenAI Vision: %w", err)
	}

	var finalResult map[string]interface{}
	json.Unmarshal([]byte(resp.Choices[0].Message.Content), &finalResult)
	return finalResult, nil
}

func (s *AIService) LivenessLuxand(image, token string) (map[string]interface{}, error) {
	b64data := image
	if idx := strings.Index(b64data, ","); idx != -1 {
		b64data = b64data[idx+1:]
	}

	decodedImg, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		return nil, fmt.Errorf("Base64 invalido: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("photo", "photo.jpg")
	part.Write(decodedImg)
	writer.Close()

	req, _ := http.NewRequest("POST", "https://api.luxand.cloud/photo/liveness", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("token", token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var luxandResp map[string]interface{}
	json.Unmarshal(respBody, &luxandResp)
	return luxandResp, nil
}
