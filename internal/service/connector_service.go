package service

import (
	"bytes"
	"encoding/json"
	"flows/internal/domain"
	"flows/internal/dto"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ConnectorService struct {
	DB *gorm.DB
}

func NewConnectorService(db *gorm.DB) *ConnectorService {
	return &ConnectorService{DB: db}
}

// ExecuteConnector performs the actual call to the external service
func (s *ConnectorService) ExecuteConnector(connectorID uint, input json.RawMessage, env string, stepConfig map[string]interface{}) (json.RawMessage, error) {
	// 1. Load Connector Definition
	var connector domain.Connector
	if err := s.DB.First(&connector, connectorID).Error; err != nil {
		return nil, fmt.Errorf("connector not found: %w", err)
	}

	// 2. Load Environment Config (Secrets)
	var config domain.ConnectorConfig
	// Default to 'development' if env not provided
	if env == "" {
		env = "development"
	}
	// In a real scenario, handle error gracefully if config missing
	s.DB.Where("connector_id = ? AND environment = ?", connectorID, env).First(&config)

	// 3. Parse Policy
	var policy dto.ConnectorPolicy
	if len(connector.Policy) > 0 {
		json.Unmarshal(connector.Policy, &policy)
	}
	// Defaults
	if policy.TimeoutMs == 0 {
		policy.TimeoutMs = 30000 // Aumentado a 30 segundos
	}
	if policy.MaxRetries == 0 {
		policy.MaxRetries = 1
	}

	// 4. Determine Target URL
	targetURL := connector.BaseURL
	if stepConfig != nil {
		if urlOverride, ok := stepConfig["url"].(string); ok && urlOverride != "" {
			// Check if it's absolute or relative
			if strings.HasPrefix(urlOverride, "http://") || strings.HasPrefix(urlOverride, "https://") {
				targetURL = urlOverride
			} else {
				// Append to BaseURL
				targetURL = strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(urlOverride, "/")
			}
		} else if route, ok := stepConfig["route"].(string); ok && route != "" {
			targetURL = strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(route, "/")
		}
	}

	client := &http.Client{
		Timeout: time.Duration(policy.TimeoutMs) * time.Millisecond,
	}

	// 5. Execute with Retries
	var resp *http.Response
	var reqErr error

	// Extraer el método HTTP de la configuración (Por defecto POST para mantener compatibilidad)
	httpMethod := "POST"
	if stepConfig != nil {
		if m, ok := stepConfig["method"].(string); ok && m != "" {
			httpMethod = strings.ToUpper(m)
		}
	}

	for i := 0; i <= policy.MaxRetries; i++ {
		var req *http.Request
		var err error

		// MAGIA ARQUITECTÓNICA: Si es GET, convierte el JSON en Query Params en la URL
		if httpMethod == "GET" {
			req, err = http.NewRequest("GET", targetURL, nil)
			if err == nil {
				var inputMap map[string]interface{}
				if errUnmarshal := json.Unmarshal(input, &inputMap); errUnmarshal == nil {
					q := req.URL.Query()
					for k, v := range inputMap {
						q.Add(k, fmt.Sprintf("%v", v))
					}
					req.URL.RawQuery = q.Encode()
				}
			}
		} else {
			// Si es POST, PUT, PATCH, manda el JSON en el Body
			req, err = http.NewRequest(httpMethod, targetURL, bytes.NewBuffer(input))
		}

		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")

		// Inyectar Headers dinámicos en cada intento
		if len(config.Config) > 0 {
			var secrets map[string]string
			json.Unmarshal(config.Config, &secrets)

			for key, value := range secrets {
				if strings.HasPrefix(key, "header_") {
					headerName := strings.TrimPrefix(key, "header_")
					req.Header.Set(headerName, value)
				}
			}

			if connector.AuthType == domain.AuthTypeAPIKey {
				if key, ok := secrets["api_key"]; ok {
					req.Header.Set("Authorization", "Bearer "+key)
				}
			}
		}

		// Log Request Dump
		reqDump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("\n--- [ConnectorService] Sending Request (Attempt %d/%d) ---\n%s\n----------------------------------------------------", i+1, policy.MaxRetries+1, string(reqDump))

		resp, reqErr = client.Do(req)
		// Si no hay error a nivel de red, o el servidor responde 4xx, rompemos el ciclo (no reintentamos)
		if reqErr == nil && resp.StatusCode < 500 {
			break
		}

		if i < policy.MaxRetries {
			log.Printf("[ConnectorService] Request failed, retrying in %dms...", policy.RetryBackoffMs)
			time.Sleep(time.Duration(policy.RetryBackoffMs) * time.Millisecond)
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("request failed after retries: %w", reqErr)
	}
	defer resp.Body.Close()

	// Log Response Dump (sin consumir el body todavía)
	respDump, _ := httputil.DumpResponse(resp, false)
	log.Printf("\n--- [ConnectorService] Received Response ---\n%s\n(Body will be read separately)\n------------------------------------------", string(respDump))

	// 6. Read Response
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		// If it's not a JSON object (e.g. array or plain text), wrap it
		var arrayResult []interface{}
		if err2 := json.Unmarshal(bodyBytes, &arrayResult); err2 == nil {
			result = map[string]interface{}{"data": arrayResult}
		} else {
			// Plain text or invalid JSON
			result = map[string]interface{}{"raw_response": string(bodyBytes)}
		}
	}

	// Add metadata
	result["_status_code"] = resp.StatusCode

	return json.Marshal(result)
}
