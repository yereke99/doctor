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
	"go.uber.org/zap"
)

// createTables creates the necessary tables if they don't exist
func createTables(db *sql.DB) error {
	query := `
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

	_, err := db.Exec(query)
	return err
}

func main() {
	zapLogger, err := logger.NewLogger()
	if err != nil {
		panic(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg, err := config.NewConfig()
	if err != nil {
		zapLogger.Error("error init config", zap.Error(err))
		return
	}
	token := cfg.Token
	// Initialize SQLite database
	db, err := sql.Open("sqlite3", "./doctor.db")
	if err != nil {
		zapLogger.Fatal("Failed to open database", zap.Error(err))
	}
	defer db.Close()

	// Create table if not exists
	if err := createTables(db); err != nil {
		zapLogger.Fatal("Failed to create tables", zap.Error(err))
	}

	doctorRepo := repository.NewDoctorRepository(db)
	redisRepo := repository.NewRedisRepository("localhost:6379", "", 0)

	h := handler.NewHandler(doctorRepo, redisRepo, zapLogger, cfg)

	opts := []bot.Option{
		bot.WithDefaultHandler(h.DefaultHandler),
		bot.WithCallbackQueryDataHandler("doctor_", bot.MatchTypePrefix, h.InlineHandlerWrapper),
		bot.WithCallbackQueryDataHandler("delete_", bot.MatchTypePrefix, h.DeleteMessageHandler),
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		zapLogger.Error("error creating bot config", zap.Error(err))
		return
	}

	go h.StartWebServer(token, ctx, b)
	zapLogger.Info("started bot")
	b.Start(ctx)
}
