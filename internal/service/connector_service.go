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
	if env == "" {
		env = "development"
	}
	s.DB.Where("connector_id = ? AND environment = ?", connectorID, env).First(&config)

	// 3. Parse Policy
	var policy dto.ConnectorPolicy
	if len(connector.Policy) > 0 {
		json.Unmarshal(connector.Policy, &policy)
	}
	if policy.TimeoutMs == 0 {
		policy.TimeoutMs = 30000
	}
	if policy.MaxRetries == 0 {
		policy.MaxRetries = 1
	}

	// 4. Determine Target URL
	targetURL := connector.BaseURL
	var routeStr string
	if stepConfig != nil {
		if urlOverride, ok := stepConfig["url"].(string); ok && urlOverride != "" {
			if strings.HasPrefix(urlOverride, "http://") || strings.HasPrefix(urlOverride, "https://") {
				targetURL = urlOverride
			} else {
				targetURL = strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(urlOverride, "/")
			}
		} else if route, ok := stepConfig["route"].(string); ok && route != "" {
			routeStr = route
			targetURL = strings.TrimRight(targetURL, "/") + "/" + strings.TrimLeft(route, "/")
		}
	}

	// REEMPLAZO DE VARIABLES DINÁMICAS EN LA URL
	var inputMap map[string]interface{}
	if err := json.Unmarshal(input, &inputMap); err == nil {
		for k, v := range inputMap {
			placeholder := fmt.Sprintf("{{input.%s}}", k)
			if strings.Contains(targetURL, placeholder) {
				targetURL = strings.ReplaceAll(targetURL, placeholder, fmt.Sprintf("%v", v))
			}
		}
	}

	client := &http.Client{
		Timeout: time.Duration(policy.TimeoutMs) * time.Millisecond,
	}

	// 5. Execute with Retries
	var resp *http.Response
	var reqErr error

	httpMethod := "POST"
	if stepConfig != nil {
		if m, ok := stepConfig["method"].(string); ok && m != "" {
			httpMethod = strings.ToUpper(m)
		}
	}

	for i := 0; i <= policy.MaxRetries; i++ {
		var req *http.Request
		var err error

		if httpMethod == "GET" {
			req, err = http.NewRequest("GET", targetURL, nil)
			if err == nil {
				if errUnmarshal := json.Unmarshal(input, &inputMap); errUnmarshal == nil {
					q := req.URL.Query()
					for k, v := range inputMap {
						placeholder := fmt.Sprintf("{{input.%s}}", k)
						// Solo inyectar como query param si NO fue usado como parte de la ruta
						if !strings.Contains(routeStr, placeholder) {
							q.Add(k, fmt.Sprintf("%v", v))
						}
					}
					req.URL.RawQuery = q.Encode()
				}
			}
		} else {
			req, err = http.NewRequest(httpMethod, targetURL, bytes.NewBuffer(input))
		}

		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")

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

		if stepConfig != nil {
			if headersMap, ok := stepConfig["headers"].(map[string]interface{}); ok {
				for k, v := range headersMap {
					req.Header.Set(k, fmt.Sprintf("%v", v))
				}
			}
		}

		reqDump, _ := httputil.DumpRequestOut(req, true)
		log.Printf("\n--- [ConnectorService] Sending Request (Attempt %d/%d) ---\n%s\n----------------------------------------------------", i+1, policy.MaxRetries+1, string(reqDump))

		resp, reqErr = client.Do(req)
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

	respDump, _ := httputil.DumpResponse(resp, false)
	log.Printf("\n--- [ConnectorService] Received Response ---\n%s\n(Body will be read separately)\n------------------------------------------", string(respDump))

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		var arrayResult []interface{}
		if err2 := json.Unmarshal(bodyBytes, &arrayResult); err2 == nil {
			result = map[string]interface{}{"data": arrayResult}
		} else {
			result = map[string]interface{}{"raw_response": string(bodyBytes)}
		}
	}

	result["_status_code"] = resp.StatusCode

	return json.Marshal(result)
}
