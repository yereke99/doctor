package main

import (
	"context"
	"os"
	"os/signal"

	"doctor/config"
	"doctor/internal/handler"
	"doctor/traits/database"
	"doctor/traits/logger"

	"github.com/go-telegram/bot"
	"go.uber.org/zap"
)

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
	_ = database.DatabaseConnection(cfg)

	token := cfg.Token
	h := handler.NewHandler(zapLogger)

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
