package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/joho/godotenv"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const serviceName = "auth-service"

// App struct (para injeção de dependência)
type App struct {
	DB         *sql.DB
	MasterKey  string
}

func main() {
	// Carrega o .env para desenvolvimento local. Em produção, isso não fará nada.
	_ = godotenv.Load()

	ctx := context.Background()

	shutdownOTel, err := setupOTel(ctx)
	if err != nil {
		slog.Error("Não foi possível inicializar a telemetria", "erro", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownOTel(shutdownCtx); err != nil {
			slog.Error("Erro no shutdown da telemetria", "erro", err)
		}
	}()

	setupLogger(serviceName)

	// --- Configuração ---
	port := os.Getenv("PORT")
	if port == "" {
		port = "8001" // Porta padrão
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		slog.Error("DATABASE_URL deve ser definida")
		os.Exit(1)
	}

	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		slog.Error("MASTER_KEY deve ser definida")
		os.Exit(1)
	}

	// --- Conexão com o Banco ---
	db, err := connectDB(databaseURL)
	if err != nil {
		slog.Error("Não foi possível conectar ao banco de dados", "erro", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Erro ao fechar conexao com o banco", "erro", err)
		}
	}()

	app := &App{
		DB:         db,
		MasterKey:  masterKey,
	}

	// --- Rotas da API ---
	mux := http.NewServeMux()
	mux.HandleFunc("/health", app.healthHandler)

	// Endpoint público para validar uma chave
	mux.HandleFunc("/validate", app.validateKeyHandler)

	// Endpoints de "admin" para criar/gerenciar chaves
	// Eles são protegidos pelo middleware de autenticação
	mux.Handle("/admin/keys", app.masterKeyAuthMiddleware(http.HandlerFunc(app.createKeyHandler)))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           instrument(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("Serviço de Autenticação (Go) iniciado", "porta", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Servidor encerrou com erro", "erro", err)
			os.Exit(1)
		}
	}()

	// Sem isto, os defers acima nunca rodam e o BatchSpanProcessor perde os
	// ultimos spans a cada deploy.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	slog.Info("Encerrando o serviço")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Erro no shutdown do servidor", "erro", err)
	}
}

// instrument embrulha o mux com o handler do OpenTelemetry: nomeia o span pela
// rota e descarta o /health, que o healthcheck bate a cada 30s.
func instrument(mux *http.ServeMux) http.Handler {
	return otelhttp.NewHandler(mux, "",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health"
		}),
	)
}

// connectDB inicializa e testa a conexão com o PostgreSQL
func connectDB(databaseURL string) (*sql.DB, error) {
	db, err := otelsql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}

	slog.Info("Conectado ao PostgreSQL com sucesso")
	return db, nil
}
