package main

import (
	"context"
	"database/sql"
	"os"
	"os/signal"

	"doctor/config"
	"doctor/internal/handler"
	"doctor/internal/repository"
	"doctor/traits/logger"

	"github.com/go-telegram/bot"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// createTables creates the necessary tables if they don't exist
func createTables(db *sql.DB) error {
	// Your existing doctor table
	doctorTable := `
	CREATE TABLE IF NOT EXISTS doctor (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		id_user INTEGER UNIQUE NOT NULL,
		fio TEXT NOT NULL,
		type_specialist TEXT NOT NULL,
		contact TEXT NOT NULL,
		ava TEXT,
		diploma TEXT,
		certificate TEXT,
		time DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_doctor_id_user ON doctor(id_user);
	CREATE INDEX IF NOT EXISTS idx_doctor_type_specialist ON doctor(type_specialist);
	`

	// Additional tables for user management
	userTables := `
	CREATE TABLE IF NOT EXISTS client (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		id_user BIGINT UNIQUE NOT NULL,
		fio TEXT,
		sex TEXT,
		problem TEXT,
		period TEXT,
		med_personal TEXT,
		contact TEXT,
		address TEXT,
		time TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_agreements (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		telegram_id BIGINT NOT NULL,
		user_type TEXT NOT NULL,
		doctor_agreement_accepted BOOLEAN DEFAULT FALSE,
		patient_agreement_accepted BOOLEAN DEFAULT FALSE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(telegram_id, user_type)
	);

	CREATE INDEX IF NOT EXISTS idx_client_user_id ON client(id_user);
	CREATE INDEX IF NOT EXISTS idx_agreements_telegram_id ON user_agreements(telegram_id);
	CREATE INDEX IF NOT EXISTS idx_agreements_user_type ON user_agreements(user_type);
	`

	// Execute doctor table creation
	if _, err := db.Exec(doctorTable); err != nil {
		return err
	}

	// Execute user tables creation
	if _, err := db.Exec(userTables); err != nil {
		return err
	}

	return nil
}

func main() {
	// Initialize logger
	zapLogger, err := logger.NewLogger()
	if err != nil {
		panic(err)
	}

	// Set up signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		zapLogger.Error("error init config", zap.Error(err))
		return
	}
	token := cfg.Token

	// Initialize SQLite database
	db, err := sql.Open("sqlite3", "./doctor.db?_foreign_keys=on")
	if err != nil {
		zapLogger.Fatal("Failed to open database", zap.Error(err))
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(); err != nil {
		zapLogger.Fatal("Failed to ping database", zap.Error(err))
	}

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		zapLogger.Fatal("Failed to create tables", zap.Error(err))
	}

	// Initialize repositories
	doctorRepo := repository.NewDoctorRepository(db)

	// Initialize user repository (uses the same database path)
	userRepo, err := repository.NewUserRepository("./doctor.db")
	if err != nil {
		zapLogger.Fatal("Failed to initialize user repository", zap.Error(err))
	}

	// Initialize Redis repository
	redisRepo := repository.NewRedisRepository("localhost:6379", "", 0)

	// Initialize handler with all repositories
	// Note: You'll need to update your NewHandler function to accept userRepo parameter
	// or use a temporary compatibility approach
	handl := handler.NewHandler(doctorRepo, userRepo, redisRepo, zapLogger, cfg)
	// Configure bot options
	opts := []bot.Option{
		bot.WithDefaultHandler(handl.DefaultHandler),
		bot.WithCallbackQueryDataHandler("doctor_", bot.MatchTypePrefix, handl.InlineHandlerWrapper),
		bot.WithCallbackQueryDataHandler("confirm_", bot.MatchTypePrefix, handl.InlineHandlerWrapper),
		bot.WithCallbackQueryDataHandler("delete_", bot.MatchTypePrefix, handl.DeleteMessageHandler),
		bot.WithCallbackQueryDataHandler("screen_", bot.MatchTypePrefix, handl.InlineScreenAnswer),  // NEW
		bot.WithCallbackQueryDataHandler("exit_chat", bot.MatchTypeExact, handl.InlineScreenAnswer), // NEW
		bot.WithMessageTextHandler("/admin", bot.MatchTypeExact, handl.AdminHandler),
		bot.WithMessageTextHandler("👥 Тіркелгендер (Just Clicked)", bot.MatchTypeExact, handl.AdminHandler),
		bot.WithMessageTextHandler("🛍 Клиенттер (Clients)", bot.MatchTypeExact, handl.AdminHandler),
		bot.WithMessageTextHandler("📢 Хабарлама (Messages)", bot.MatchTypeExact, handl.AdminHandler),
		bot.WithMessageTextHandler("📊 Статистика (Statistics)", bot.MatchTypeExact, handl.AdminHandler),
		bot.WithMessageTextHandler("❌ Жабу (Close)", bot.MatchTypeExact, handl.AdminHandler),
	}

	// Create bot instance
	b, err := bot.New(token, opts...)
	if err != nil {
		zapLogger.Error("error creating bot", zap.Error(err))
		return
	}

	// Start web server in a separate goroutine
	go func() {
		zapLogger.Info("starting web server on :8080")
		handl.StartWebServer(token, ctx, b)
	}()

	// Start the bot
	zapLogger.Info("bot started successfully")
	b.Start(ctx)

	// Cleanup
	zapLogger.Info("shutting down...")
}
