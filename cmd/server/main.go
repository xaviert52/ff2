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
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /api/v1

func main() {
	// 1. Intentar cargar .env desde el directorio actual
	if err := godotenv.Load(); err != nil {
		log.Println("No .env found in current directory, checking executable directory...")

		// 2. Si falla, intentar cargar desde el directorio del binario
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

	// Initialize Services
	connHandler := handler.NewConnectorHandler(database)
	stepValidator := service.NewStepValidator()
	subscriptionClient := service.NewSubscriptionClientFromEnv()
	connectorService := service.NewConnectorService(database)
	flowManager := service.NewFlowManager(database, stepValidator, subscriptionClient, connectorService)

	// AI Service
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Println("Warning: OPENAI_API_KEY not set. AI features will fail.")
	}
	aiService := service.NewAIService(apiKey)

	flowHandler := handler.NewFlowHandler(flowManager)
	aiHandler := handler.NewAIHandler(aiService)

	r := gin.Default()

	// CORS for all domains and headers
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	api := r.Group("/api/v1")
	{
		// Connector Routes
		api.POST("/connectors", connHandler.CreateConnector)
		api.GET("/connectors", connHandler.ListConnectors)
		api.POST("/connectors/config", connHandler.CreateConfig)

		// Flow Management
		api.POST("/flows", flowHandler.CreateFlow)
		api.GET("/flows", flowHandler.ListFlows)

		// Flow Execution Routes
		api.POST("/flows/:id/start", flowHandler.StartFlow)
		api.GET("/executions/:uuid/step", flowHandler.GetCurrentStep)
		api.POST("/executions/:uuid/step", flowHandler.SubmitStep)

		// Execution Management Routes
		api.GET("/executions/:uuid", flowHandler.GetExecution)
		api.POST("/executions/:uuid/retry", flowHandler.RetryExecution)

		// AI Processing
		api.POST("/ai/generate", aiHandler.GenerateFlow)
		api.POST("/ai/signature-analysis", aiHandler.AnalyzeSignature)
		api.POST("/ai/liveness-luxand", aiHandler.LivenessLuxand)
	}

	// Configurar Swagger
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

	notifyApiURL := notifyURL + "/api/v1"
	pdfApiURL := pdfURL + "/api/v1"

	// Upsert de todos los Conectores (Asegurando AuthType: "NONE")
	connectors := []domain.Connector{
		{ID: 1, Name: "OTP Generator", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 2, Name: "WhatsApp Notify", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 3, Name: "OTP Extractor", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 4, Name: "OTP Validator", Type: "REST", BaseURL: notifyApiURL, AuthType: "NONE"},
		{ID: 5, Name: "PDF Generator", Type: "REST", BaseURL: pdfApiURL, AuthType: "NONE"},
		{ID: 96, Name: "Firmador", Type: "REST", BaseURL: "https://front.primecore.online/signer", AuthType: "NONE"},
		{ID: 97, Name: "RA Certificados", Type: "REST", BaseURL: "https://front.primecore.online/ra", AuthType: "NONE"},
		{ID: 98, Name: "Biometria", Type: "REST", BaseURL: "https://front.primecore.online/biometria", AuthType: "NONE"},
		{ID: 99, Name: "Registro Civil", Type: "REST", BaseURL: "https://front.primecore.online/reg-civil", AuthType: "NONE"},
		{ID: 102, Name: "Archivos Primecore", Type: "REST", BaseURL: "https://files.primecore.online", AuthType: "NONE"},
		{ID: 103, Name: "AI Services Local", Type: "REST", BaseURL: "http://localhost:8080/api/v1", AuthType: "NONE"},
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

	flow2Definition := `{
  "name": "Flujo de Emisión de Certificados",
  "start_step": "solicitar_pago",
  "steps": {
    "solicitar_pago": { "id": "solicitar_pago", "type": "FORM", "description": "Simulación de Pago", "schema": { "type": "object", "properties": { "voucher": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "solicitar_datos" } ] },
    "solicitar_datos": { "id": "solicitar_datos", "type": "FORM", "description": "Ingreso de Cédula, RUC y Contacto", "schema": { "type": "object", "properties": { "cedula": { "type": "string" }, "codigo_dactilar": { "type": "string" }, "ruc": { "type": "string" }, "telefono": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "consultar_registro_civil" } ] },
    "consultar_registro_civil": { "id": "consultar_registro_civil", "type": "ACTION", "connector_id": 99, "config": { "route": "/consulta", "method": "GET" }, "input_mapping": { "nui": "{{input.cedula}}", "dactilar": "{{input.codigo_dactilar}}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "crear_ficha_pdf" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_datos" } ] },
    "crear_ficha_pdf": { "id": "crear_ficha_pdf", "type": "ACTION", "connector_id": 5, "config": { "route": "/convert", "method": "POST" }, "input_mapping": { "filename": "ficha.pdf", "file_type": "html", "content": "PGh0bWw+PGJvZHk+PGI+RmljaGEgZGUgQ2xpZW50ZTwvYj48YnI+PGI+UnVjPC9iPiB7e2lucHV0LnJ1Y319PGJyPjxiPlRlbGVmb25vOjwvYj4ge3tpbnB1dC50ZWxlZm9ub319PGJyPjxwIHN0eWxlPSJjb2xvcjp3aGl0ZSI+ZmlybWFfY2xpZW50ZTwvcD48L2JvZHk+PC9odG1sPg==" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "crear_contrato_pdf" } ] },
    "crear_contrato_pdf": { "id": "crear_contrato_pdf", "type": "ACTION", "connector_id": 5, "config": { "route": "/convert", "method": "POST" }, "input_mapping": { "filename": "contrato.pdf", "file_type": "html", "content": "PGh0bWw+PGJvZHk+PGI+Q29udHJhdG8gZGUgU2VydmljaW9zPC9iPjxicj48Yj5SdWNDPC9iPiB7e2lucHV0LnJ1Y319PGJyPjxwIHN0eWxlPSJjb2xvcjp3aGl0ZSI+ZmlybWFfY2xpZW50ZTwvcD48L2JvZHk+PC9odG1sPg==" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "solicitar_firma" } ] },
    "solicitar_firma": { "id": "solicitar_firma", "type": "FORM", "description": "Carga de Firma Manuscrita", "schema": { "type": "object", "properties": { "firma_b64": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "validar_firma_ia" } ] },
    "validar_firma_ia": { "id": "validar_firma_ia", "type": "ACTION", "connector_id": 103, "config": { "route": "/ai/signature-analysis", "method": "POST" }, "input_mapping": { "capturedImage": "{{input.firma_b64}}", "referenceImage": "{{output.consultar_registro_civil.foto_url}}" }, "transitions": [ { "condition": "{{output.overallStatus}} == \"verified\"", "next_step": "solicitar_biometria" }, { "condition": "{{output.overallStatus}} != \"verified\"", "next_step": "solicitar_firma" } ] },
    "solicitar_biometria": { "id": "solicitar_biometria", "type": "FORM", "description": "Carga de Selfie", "schema": { "type": "object", "properties": { "foto_b64": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "validar_liveness_luxand" } ] },
    "validar_liveness_luxand": { "id": "validar_liveness_luxand", "type": "ACTION", "connector_id": 103, "config": { "route": "/ai/liveness-luxand", "method": "POST" }, "input_mapping": { "image": "{{input.foto_b64}}" }, "transitions": [ { "condition": "{{output.status}} == \"success\"", "next_step": "verificar_biometria" }, { "condition": "{{output.status}} != \"success\"", "next_step": "solicitar_biometria" } ] },
    "verificar_biometria": { "id": "verificar_biometria", "type": "ACTION", "connector_id": 98, "config": { "route": "/api/biometrics/demo_validation_service", "method": "POST" }, "input_mapping": { "uuidProceso": "{{global.transaction_id}}", "cedulaFrontalBase64": "{{output.consultar_registro_civil.foto_url}}", "rostroPersonaBase64": "{{input.foto_b64}}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "solicitar_contrasena" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_biometria" } ] },
    "solicitar_contrasena": { "id": "solicitar_contrasena", "type": "FORM", "schema": { "type": "object", "properties": { "password": { "type": "string" } } }, "transitions": [ { "condition": "true", "next_step": "crear_certificado" } ] },
    "crear_certificado": { "id": "crear_certificado", "type": "ACTION", "connector_id": 97, "config": { "route": "/certificados/crea/normal", "method": "POST" }, "input_mapping": { "perfil": "012", "alias": "{{global.transaction_id}}", "pass": "{{input.password}}", "cedulaPasaporte": "{{output.consultar_registro_civil.nui}}", "nombres": "{{output.consultar_registro_civil.nombres}}", "apellido1": "{{output.consultar_registro_civil.apellidos}}", "apellido2": ".", "direccion": ".", "telefono": "{{steps.solicitar_datos.telefono}}", "ciudad": "QUITO", "pais": "EC", "politica": true, "servidor": 3, "email": "{{global.email}}" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "descargar_certificado_p12" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_contrasena" } ] },
    "descargar_certificado_p12": { "id": "descargar_certificado_p12", "type": "ACTION", "connector_id": 102, "config": { "route": "/documentos/base64/normal/{{output.crear_certificado.data}}", "method": "GET" }, "transitions": [ { "condition": "true", "next_step": "firmar_documentos" } ] },
    "firmar_documentos": { "id": "firmar_documentos", "type": "ACTION", "connector_id": 96, "config": { "route": "/api/v1/sign/pdf", "method": "POST" }, "input_mapping": { "base64Pdf": "{{output.crear_ficha_pdf.base64_data}}", "base64P12": "{{output.descargar_certificado_p12.archivo}}", "password": "{{input.password}}", "typeSignature": "QR", "textMarker": "firma_cliente" }, "transitions": [ { "condition": "{{output._status_code}} < 400", "next_step": "enviar_correo" }, { "condition": "{{output._status_code}} >= 400", "next_step": "solicitar_contrasena" } ] },
    "enviar_correo": { "id": "enviar_correo", "type": "ACTION", "connector_id": 2, "config": { "route": "/notify", "method": "POST" }, "input_mapping": { "to": "{{global.email}}", "pdf": "{{output.firmar_documentos.signed_pdf}}" } }
  }
}`

	flow2 := domain.Flow{
		ID:          2,
		Name:        "Flujo de Emisión de Certificados",
		Description: "Flujo E2E validado en Produccion",
		Definition:  []byte(flow2Definition),
	}
	database.Save(&flow2) // Upsert del flujo 2
	log.Println("Database seeding completed.")
}
