package service

import (
	"encoding/json"
	"errors"
	"flows/internal/domain"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type FlowManager struct {
	DB                 *gorm.DB
	StepValidator      *StepValidator
	SubscriptionClient *SubscriptionClient
	ConnectorService   *ConnectorService
}

func NewFlowManager(db *gorm.DB, validator *StepValidator, subscriptionClient *SubscriptionClient, connectorService *ConnectorService) *FlowManager {
	return &FlowManager{
		DB:                 db,
		StepValidator:      validator,
		SubscriptionClient: subscriptionClient,
		ConnectorService:   connectorService,
	}
}

func resolveTemplate(template string, context map[string]interface{}) interface{} {
	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)

	if re.MatchString(template) {
		matches := re.FindStringSubmatch(template)
		if len(matches) == 2 && matches[0] == template {
			return getValueFromPath(matches[1], context)
		}
	}

	return re.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
		val := getValueFromPath(path, context)
		if val == nil {
			return ""
		}
		return fmt.Sprintf("%v", val)
	})
}

func getValueFromPath(path string, context map[string]interface{}) interface{} {
	parts := strings.Split(path, ".")
	var current interface{} = context

	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			if val, exists := m[part]; exists {
				current = val
			} else {
				return nil
			}
		} else if l, ok := current.([]interface{}); ok {
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err == nil {
				if idx >= 0 && idx < len(l) {
					current = l[idx]
				} else {
					return nil
				}
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	return current
}

func (m *FlowManager) StartFlow(flowID uint, input map[string]interface{}) (*domain.Execution, error) {
	var flow domain.Flow
	if err := m.DB.First(&flow, flowID).Error; err != nil {
		return nil, err
	}

	var definition domain.FlowDefinition
	if err := json.Unmarshal(flow.Definition, &definition); err != nil {
		return nil, fmt.Errorf("invalid flow definition: %w", err)
	}

	if definition.StartStep == "" {
		return nil, errors.New("flow definition has no start step")
	}

	inputJSON, _ := json.Marshal(input)
	execID := uuid.New().String()

	exec := &domain.Execution{
		ID:          execID,
		FlowID:      flowID,
		Status:      domain.StatusPending,
		CurrentStep: definition.StartStep,
		Data:        inputJSON,
		StepsData:   json.RawMessage("{}"),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := m.DB.Create(exec).Error; err != nil {
		return nil, err
	}

	if m.SubscriptionClient != nil {
		m.SubscriptionClient.SendStatus(exec)
	}

	startStep := definition.Steps[definition.StartStep]
	if startStep.Type == domain.StepTypeAction || startStep.Type == domain.StepTypeDecision {
		log.Printf("[FlowManager] Start step '%s' is automatic (%s). Executing asynchronously...", startStep.ID, startStep.Type)

		exec.Status = domain.StatusRunning
		m.DB.Save(exec)

		go func(executionID string) {
			_, err := m.SubmitStep(executionID, []byte("{}"))
			if err != nil {
				log.Printf("[FlowManager] Async execution failed for %s: %v", executionID, err)
			}
		}(exec.ID)

		return exec, nil
	}

	return exec, nil
}

func (m *FlowManager) GetCurrentStep(execID string) (*domain.Step, *domain.Execution, error) {
	var exec domain.Execution
	if err := m.DB.First(&exec, "id = ?", execID).Error; err != nil {
		return nil, nil, err
	}

	if exec.Status == domain.StatusCompleted || exec.Status == domain.StatusFailed {
		return nil, &exec, errors.New("execution is already finished")
	}

	var flow domain.Flow
	if err := m.DB.First(&flow, exec.FlowID).Error; err != nil {
		return nil, nil, err
	}

	var definition domain.FlowDefinition
	if err := json.Unmarshal(flow.Definition, &definition); err != nil {
		return nil, nil, err
	}

	step, ok := definition.Steps[exec.CurrentStep]
	if !ok {
		return nil, nil, fmt.Errorf("step %s not found in definition", exec.CurrentStep)
	}

	return &step, &exec, nil
}

func (m *FlowManager) ListFlows() ([]domain.Flow, error) {
	var flows []domain.Flow
	if err := m.DB.Find(&flows).Error; err != nil {
		return nil, err
	}
	return flows, nil
}

func (m *FlowManager) RetryExecution(execID string, newData map[string]interface{}) (*domain.Execution, error) {
	var exec domain.Execution
	if err := m.DB.First(&exec, "id = ?", execID).Error; err != nil {
		return nil, err
	}

	if exec.Status != domain.StatusFailed {
		return &exec, errors.New("only failed executions can be retried")
	}

	if len(newData) > 0 {
		var currentData map[string]interface{}
		json.Unmarshal(exec.Data, &currentData)
		if currentData == nil {
			currentData = make(map[string]interface{})
		}
		for k, v := range newData {
			currentData[k] = v
		}
		dataJSON, _ := json.Marshal(currentData)
		exec.Data = dataJSON
	}

	exec.Status = domain.StatusRunning
	exec.UpdatedAt = time.Now()
	if err := m.DB.Save(&exec).Error; err != nil {
		return nil, err
	}

	if m.SubscriptionClient != nil {
		m.SubscriptionClient.SendStatus(&exec)
	}

	return m.SubmitStep(exec.ID, []byte("{}"))
}

func (m *FlowManager) GetExecutionDetails(execID string) (*domain.Execution, error) {
	var exec domain.Execution
	if err := m.DB.First(&exec, "id = ?", execID).Error; err != nil {
		return nil, err
	}
	return &exec, nil
}

func (m *FlowManager) SubmitStep(execID string, input json.RawMessage) (*domain.Execution, error) {
	var exec domain.Execution
	if err := m.DB.First(&exec, "id = ?", execID).Error; err != nil {
		return nil, err
	}

	var flow domain.Flow
	if err := m.DB.First(&flow, exec.FlowID).Error; err != nil {
		return nil, err
	}

	var definition domain.FlowDefinition
	if err := json.Unmarshal(flow.Definition, &definition); err != nil {
		return nil, err
	}

	for {
		log.Printf("[FlowManager] Processing loop for execution %s", exec.ID)

		step, ok := definition.Steps[exec.CurrentStep]
		if !ok {
			return nil, fmt.Errorf("step %s not found in definition", exec.CurrentStep)
		}

		log.Printf("[FlowManager] Current step: %s (Type: %s)", step.ID, step.Type)

		if len(step.InputMapping) > 0 {
			mappingContext := make(map[string]interface{})

			var globalInput map[string]interface{}
			json.Unmarshal(exec.Data, &globalInput)
			mappingContext["global"] = globalInput

			var stepsData map[string]interface{}
			json.Unmarshal(exec.StepsData, &stepsData)
			mappingContext["steps"] = stepsData

			var currentInputMap map[string]interface{}
			json.Unmarshal(input, &currentInputMap)
			mappingContext["input"] = currentInputMap

			mappedInput := make(map[string]interface{})
			for k, v := range step.InputMapping {
				if strVal, ok := v.(string); ok {
					mappedInput[k] = resolveTemplate(strVal, mappingContext)
				} else {
					mappedInput[k] = v
				}
			}
			mappedBytes, _ := json.Marshal(mappedInput)
			input = mappedBytes
		}

		var outputData json.RawMessage

		switch step.Type {
		case domain.StepTypeForm:
			schemaJSON, _ := json.Marshal(step.Schema)
			if err := m.StepValidator.Validate(schemaJSON, input); err != nil {
				return nil, fmt.Errorf("validation error: %w", err)
			}
			outputData = input

		case domain.StepTypeAction:
			result, err := m.ConnectorService.ExecuteConnector(step.ConnectorID, input, "development", step.Config)
			if err != nil {
				exec.Status = domain.StatusFailed
				m.DB.Save(&exec)
				return nil, fmt.Errorf("connector failed: %w", err)
			}
			outputData = result

		case domain.StepTypeDecision:
			outputData = input
		}

		var currentStepsData map[string]interface{}
		json.Unmarshal(exec.StepsData, &currentStepsData)
		if currentStepsData == nil {
			currentStepsData = make(map[string]interface{})
		}

		var outputMap interface{}
		json.Unmarshal(outputData, &outputMap)
		currentStepsData[exec.CurrentStep] = outputMap

		newStepsData, _ := json.Marshal(currentStepsData)
		exec.StepsData = newStepsData

		// --- Evaluar Transiciones Condicionales ---
		nextStepID := step.NextStep
		transitionMatched := false

		if len(step.Transitions) > 0 {
			transitionContext := make(map[string]interface{})

			var globalInput map[string]interface{}
			json.Unmarshal(exec.Data, &globalInput)
			transitionContext["global"] = globalInput

			var stepsData map[string]interface{}
			json.Unmarshal(exec.StepsData, &stepsData)
			transitionContext["steps"] = stepsData

			var currentOutput map[string]interface{}
			json.Unmarshal(outputData, &currentOutput)
			transitionContext["output"] = currentOutput

			for _, t := range step.Transitions {
				resolvedCondition := resolveTemplate(t.Condition, transitionContext)
				conditionStr := fmt.Sprintf("%v", resolvedCondition)

				if conditionStr == "true" {
					nextStepID = t.NextStep
					transitionMatched = true
					break
				}

				// Ordenados por longitud para evitar que "<" intercepte a "<="
				operators := []string{">=", "<=", "==", "!=", ">", "<"}
				for _, op := range operators {
					if strings.Contains(conditionStr, op) {
						parts := strings.Split(conditionStr, op)
						if len(parts) == 2 {
							left := strings.TrimSpace(parts[0])
							right := strings.TrimSpace(parts[1])

							leftNum, err1 := strconv.ParseFloat(left, 64)
							rightNum, err2 := strconv.ParseFloat(right, 64)

							if err1 == nil && err2 == nil {
								// Comparación numérica (ideal para códigos HTTP)
								switch op {
								case "==":
									transitionMatched = leftNum == rightNum
								case "!=":
									transitionMatched = leftNum != rightNum
								case ">=":
									transitionMatched = leftNum >= rightNum
								case "<=":
									transitionMatched = leftNum <= rightNum
								case ">":
									transitionMatched = leftNum > rightNum
								case "<":
									transitionMatched = leftNum < rightNum
								}
							} else {
								// Fallback a comparación de texto
								if op == "==" {
									transitionMatched = left == right
								}
								if op == "!=" {
									transitionMatched = left != right
								}
							}

							if transitionMatched {
								nextStepID = t.NextStep
								break
							}
						}
					}
				}
				if transitionMatched {
					break
				}
			}
		}

		// --- Hard Stop (Protección de Arquitectura) ---
		if step.Type == domain.StepTypeAction {
			var currentOutput map[string]interface{}
			json.Unmarshal(outputData, &currentOutput)

			if statusCodeVal, exists := currentOutput["_status_code"]; exists {
				statusCodeStr := fmt.Sprintf("%v", statusCodeVal)
				if strings.Contains(statusCodeStr, ".") {
					statusCodeStr = strings.Split(statusCodeStr, ".")[0]
				}
				statusCode, _ := strconv.Atoi(statusCodeStr)

				// Si hubo un error HTTP y no se definió una transición de rescate
				if statusCode >= 400 && !transitionMatched && len(step.Transitions) == 0 {
					log.Printf("[FlowManager] Hard Stop triggered for step %s. HTTP Status: %d", step.ID, statusCode)
					exec.Status = domain.StatusFailed
					exec.UpdatedAt = time.Now()
					m.DB.Save(&exec)
					return nil, fmt.Errorf("hard stop: el conector '%s' falló con código HTTP %d", step.ID, statusCode)
				}
			}
		}

		if nextStepID == "" {
			exec.Status = domain.StatusCompleted
			exec.CurrentStep = ""
			exec.UpdatedAt = time.Now()

			m.DB.Save(&exec)
			if m.SubscriptionClient != nil {
				m.SubscriptionClient.SendStatus(&exec)
			}
			return &exec, nil
		}

		exec.CurrentStep = nextStepID
		exec.Status = domain.StatusRunning
		exec.UpdatedAt = time.Now()

		nextStepDef := definition.Steps[nextStepID]
		if nextStepDef.Type == domain.StepTypeForm {
			exec.Status = domain.StatusWaiting
			m.DB.Save(&exec)
			if m.SubscriptionClient != nil {
				m.SubscriptionClient.SendStatus(&exec)
			}
			return &exec, nil
		}

		input = outputData
	}
}
