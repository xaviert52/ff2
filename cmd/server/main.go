package main

import (
	_ "flows/docs"
	"flows/internal/domain"
	"flows/internal/handler"
	"flows/internal/infrastructure/db"
	"flows/internal/service"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"
)

// @title Extended Flow Management System API
// @version 1.0
// @description API for managing connectors and executing flows step-by-step.
// @host localhost:8080
// @BasePath /api/v1

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env found in current directory, checking executable directory...")
		ex, err := os.Executable()
		if err == nil {
			exPath := filepath.Dir(ex)
			envPath := filepath.Join(exPath, ".env")
			if err := godotenv.Load(envPath); err != nil {
				log.Printf("No .env found at %s either. Using environment variables.\n", envPath)
			} else {
				log.Printf("Loaded .env from executable directory: %s\n", envPath)
			}
		}
	} else {
		log.Println("Loaded .env from current directory")
	}

	database, err := db.NewDB()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	SeedDatabase(database)

	connHandler := handler.NewConnectorHandler(database)
	stepValidator := service.NewStepValidator()
	subscriptionClient := service.NewSubscriptionClientFromEnv()
	connectorService := service.NewConnectorService(database)
	flowManager := service.NewFlowManager(database, stepValidator, subscriptionClient, connectorService)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Println("Warning: OPENAI_API_KEY not set. AI features will fail.")
	}
	aiService := service.NewAIService(apiKey)

	flowHandler := handler.NewFlowHandler(flowManager)
	aiHandler := handler.NewAIHandler(aiService)

	r := gin.Default()
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	api := r.Group("/api/v1")
	{
		api.POST("/connectors", connHandler.CreateConnector)
		api.GET("/connectors", connHandler.ListConnectors)
		api.POST("/connectors/config", connHandler.CreateConfig)

		api.POST("/flows", flowHandler.CreateFlow)
		api.GET("/flows", flowHandler.ListFlows)

		api.POST("/flows/:id/start", flowHandler.StartFlow)
		api.GET("/executions/:uuid/step", flowHandler.GetCurrentStep)
		api.POST("/executions/:uuid/step", flowHandler.SubmitStep)

		api.GET("/executions/:uuid", flowHandler.GetExecution)
		api.POST("/executions/:uuid/retry", flowHandler.RetryExecution)

		api.POST("/ai/generate", aiHandler.GenerateFlow)
		api.POST("/ai/signature-analysis", aiHandler.AnalyzeSignature)
		api.POST("/ai/liveness-luxand", aiHandler.LivenessLuxand)
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r.Run(":" + port)
}

func SeedDatabase(database *gorm.DB) {
	log.Println("Seeding database configuration...")

	notifyURL := os.Getenv("NOTIFY_SERVICE_URL")
	if notifyURL == "" {
		notifyURL = "http://host.docker.internal:8081"
	}
	pdfURL := os.Getenv("PDF_SERVICE_URL")
	if pdfURL == "" {
		pdfURL = "http://host.docker.internal:8082"
	}
	apisixURL := os.Getenv("APISIX_URL")
	if apisixURL == "" {
		apisixURL = "http://34.217.51.225:9080"
	}

	notifyApiURL := notifyURL + "/api/v1"
	pdfApiURL := pdfURL + "/api/v1"

	connectors := []domain.Connector{
		{ID: 1, Name: "OTP Generator", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 2, Name: "WhatsApp Notify", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 3, Name: "OTP Extractor", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 4, Name: "OTP Validator", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 5, Name: "PDF Generator", Type: "REST", BaseURL: pdfApiURL, AuthType: "NONE"},
		{ID: 96, Name: "Firmador", Type: "REST", BaseURL: apisixURL + "/signer", AuthType: "NONE"},
		{ID: 97, Name: "RA Certificados", Type: "REST", BaseURL: apisixURL + "/ra", AuthType: "NONE"},
		{ID: 98, Name: "Biometria", Type: "REST", BaseURL: apisixURL + "/biometria", AuthType: "NONE"},
		{ID: 99, Name: "Registro Civil", Type: "REST", BaseURL: apisixURL + "/reg-civil", AuthType: "NONE"},
		{ID: 102, Name: "Archivos Primecore", Type: "REST", BaseURL: apisixURL + "/files", AuthType: "NONE"},
		{ID: 103, Name: "AI Services Local", Type: "REST", BaseURL: "http://ai_service:8080/api", AuthType: "NONE"},
		{ID: 104, Name: "Payment Service", Type: "REST", BaseURL: "http://payment_service:8080", AuthType: "NONE"},
	}

	for _, c := range connectors {
		database.Save(&c)
	}

	var count int64
	database.Model(&domain.Flow{}).Where("id = ?", 1).Count(&count)
	if count == 0 {
		flow1Definition := `{
			"start_step": "create_otp",
			"steps": {
				"create_otp": { "id": "create_otp", "type": "ACTION", "connector_id": 1, "config": {"route": "/otp/generate"}, "input_mapping": { "account_name": "{{global.phone_number}}", "period_minutes": 5 }, "next_step": "extract_otp" },
				"extract_otp": { "id": "extract_otp", "type": "ACTION", "connector_id": 3, "config": {"route": "/otp/code/from_url"}, "input_mapping": { "url": "{{steps.create_otp.data.0.url}}" }, "next_step": "send_whatsapp" },
				"send_whatsapp": { "id": "send_whatsapp", "type": "ACTION", "connector_id": 2, "config": {"route": "/notify"}, "input_mapping": { "type": "whatsapp", "recipient": "{{global.phone_number}}", "body": "Tu codigo de verificacion es: {{steps.extract_otp.data.0.code}}" }, "next_step": "ask_otp" },
				"ask_otp": { "id": "ask_otp", "type": "FORM", "next_step": "validate_otp" },
				"validate_otp": { "id": "validate_otp", "type": "ACTION", "connector_id": 4, "config": {"route": "/otp/validate"}, "input_mapping": { "code": "{{input.code}}", "secret": "{{steps.create_otp.data.0.secret}}", "period_minutes": 5 }, "next_step": "final_message" },
				"final_message": { "id": "final_message", "type": "ACTION", "connector_id": 2, "config": {"route": "/notify"}, "input_mapping": { "type": "whatsapp", "recipient": "{{global.phone_number}}", "body": "✅ ¡Felicidades! Verificación exitosa." } }
			}
		}`
		flow1 := domain.Flow{
			ID:          1,
			Name:        "WhatsApp OTP Verification",
			Description: "E2E flow for generating, sending, and validating OTP via WhatsApp",
			Definition:  []byte(flow1Definition),
		}
		database.Save(&flow1)
	}

	// FLUJO 2: CERTIFICADOS FRONTEND
	flow2Definition := `{
  "name": "Flujo de Emisión de Certificados",
  "start_step": "solicitar_pago_form",
  "steps": {
    "solicitar_pago_form": { "id": "solicitar_pago_form", "type": "FORM", "description": "Formulario de Pago", "schema": { "type": "object", "properties": { "metodo_pago": { "type": "string" }, "cliente_id": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "procesar_pago_api" } ] },
    "procesar_pago_api": { "id": "procesar_pago_api", "type": "ACTION", "connector_id": 104, "config": { "route": "/v1/payments", "method": "POST" }, "input_mapping": { "amount": 10.50, "currency": "USD", "client_id": "{{input.cliente_id}}", "reference_id": "{{global.transaction_id}}", "method": "{{input.metodo_pago}}", "metadata": "{}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "solicitar_datos" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_pago_form" } ] },
    "solicitar_datos": { "id": "solicitar_datos", "type": "FORM", "description": "Ingreso de Cédula, Dactilar, Teléfono, Correo", "schema": { "type": "object", "properties": { "cedula": { "type": "string" }, "codigo_dactilar": { "type": "string" }, "telefono": { "type": "string" }, "correo": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "solicitar_biometria" } ] },
    "solicitar_biometria": { "id": "solicitar_biometria", "type": "FORM", "description": "Carga de Cédula Frontal, Reverso y Selfie", "schema": { "type": "object", "properties": { "cedula_frontal_b64": { "type": "string" }, "cedula_reverso_b64": { "type": "string" }, "selfie_b64": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "verificar_biometria" } ] },
    "verificar_biometria": { "id": "verificar_biometria", "type": "ACTION", "connector_id": 98, "config": { "route": "/api/biometrics/demo_validation_extended", "method": "POST" }, "input_mapping": { "uuidProceso": "{{global.transaction_id}}", "cedulaFrontalBase64": "data:image/jpeg;base64,{{input.cedula_frontal_b64}}", "rostroPersonaBase64": "data:image/jpeg;base64,{{input.selfie_b64}}" }, "transitions": [ { "condition": "{{output.status}} == true", "next_step": "solicitar_contrasena" }, { "condition": "{{output.status}} == false", "next_step": "solicitar_biometria" } ] },
    "solicitar_contrasena": { "id": "solicitar_contrasena", "type": "FORM", "schema": { "type": "object", "properties": { "password": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "crear_certificado" } ] },
    "crear_certificado": { "id": "crear_certificado", "type": "ACTION", "connector_id": 97, "config": { "route": "/certificados/crea/normal", "method": "POST" }, "input_mapping": { "perfil": "012", "alias": "{{global.transaction_id}}", "pass": "{{input.password}}", "cedulaPasaporte": "{{steps.solicitar_datos.cedula}}", "nombres": "USUARIO", "apellido1": "FCE1", "apellido2": ".", "direccion": ".", "telefono": "{{steps.solicitar_datos.telefono}}", "ciudad": "QUITO", "pais": "EC", "politica": true, "servidor": 3, "email": "{{steps.solicitar_datos.correo}}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "descargar_certificado_p12" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_contrasena" } ] },
    "descargar_certificado_p12": { "id": "descargar_certificado_p12", "type": "ACTION", "connector_id": 102, "config": { "route": "/documentos/base64/normal/{{output.crear_certificado.data}}", "method": "GET" }, "transitions": [ { "condition": "true", "next_step": "finalizado_fce1" } ] },
    "finalizado_fce1": { "id": "finalizado_fce1", "type": "FORM", "description": "Certificado Generado Exitosamente", "schema": { "type": "object", "properties": {} } }
  }
}`
	flow2 := domain.Flow{
		ID:          2,
		Name:        "Flujo de Emisión de Certificados",
		Description: "Recolección de datos, Pagos, Biometría Cédula-Rostro y Emisión",
		Definition:  []byte(flow2Definition),
	}
	database.Save(&flow2)

	// FLUJO 3: ONBOARDING FIRMAS FRONTEND
	flow3Definition := `{
  "name": "Flujo de Onboarding Firmas",
  "start_step": "solicitar_cedula_fo1",
  "steps": {
    "solicitar_cedula_fo1": { "id": "solicitar_cedula_fo1", "type": "FORM", "description": "Ingreso de Cédula y Dactilar para Validar Firma", "schema": { "type": "object", "properties": { "cedula": { "type": "string" }, "codigo_dactilar": { "type": "string" }, "telefono": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "consultar_rc_firmas" } ] },
    "consultar_rc_firmas": { "id": "consultar_rc_firmas", "type": "ACTION", "connector_id": 99, "config": { "route": "/consulta", "method": "GET" }, "input_mapping": { "nui": "{{input.cedula}}", "dactilar": "{{input.codigo_dactilar}}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "descargar_firma_rc" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_cedula_fo1" } ] },
    "descargar_firma_rc": { "id": "descargar_firma_rc", "type": "ACTION", "connector_id": 102, "config": { "route": "/documentos/base64/registro_civil/{{steps.solicitar_cedula_fo1.cedula}}_firma", "method": "GET" }, "transitions": [ { "condition": "true", "next_step": "solicitar_captura_firma" } ] },
    "solicitar_captura_firma": { "id": "solicitar_captura_firma", "type": "FORM", "description": "Sube una foto de tu firma manuscrita", "schema": { "type": "object", "properties": { "firma_b64": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "validar_firma_ia" } ] },
    "validar_firma_ia": { "id": "validar_firma_ia", "type": "ACTION", "connector_id": 103, "config": { "route": "/signature-analysis", "method": "POST" }, "input_mapping": { "capturedImage": "data:image/jpeg;base64,{{input.firma_b64}}", "referenceImage": "data:image/jpeg;base64,{{steps.descargar_firma_rc.archivo}}" }, "transitions": [ { "condition": "{{output.overallStatus}} == \"verified\"", "next_step": "finalizado_fo1" }, { "condition": "{{output.overallStatus}} != \"verified\"", "next_step": "solicitar_captura_firma" } ] },
    "finalizado_fo1": { "id": "finalizado_fo1", "type": "FORM", "description": "Firma Validada Exitosamente", "schema": { "type": "object", "properties": {} } }
  }
}`
	flow3 := domain.Flow{
		ID:          3,
		Name:        "Flujo de Onboarding Firmas",
		Description: "Validación de Foto de Firma contra Registro Civil",
		Definition:  []byte(flow3Definition),
	}
	database.Save(&flow3)

	log.Println("Database seeding completed.")
}
