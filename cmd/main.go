package main

import (
	"context"
	"doctor/config"
	"doctor/traits/logger"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
	}

	token := cfg.Token

	b, err := bot.New(token, opts...)
	if err != nil {
		zapLogger.Error("error creating bot config", zap.Error(err))
		return
	}

	// Передаём экземпляр бота в веб-сервер.
	go startWebServer(cfg.Token)
	zapLogger.Info("started bot")
	b.Start(ctx)
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	// Извлекаем user_id из сообщения и конвертируем его в строку.
	//userIDStr := strconv.FormatInt(update.Message.From.ID, 10)
	// Формируем URL, добавляя user_id как параметр запроса.
	//webAppURL := "https://60d2a8a97c10e8ff67f9ab2f87aaf166.serveo.net/?user_id=" + userIDStr
	webAppURL := "https://e423-89-219-13-135.ngrok-free.app/doctor"
	// Создаем inline-клавиатуру с кнопкой, открывающей мини-приложение
	keyboard := models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text: "Открыть мини-приложение для врача",
					WebApp: &models.WebAppInfo{
						URL: webAppURL,
					},
				},
			},
		},
	}

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        "Нажмите кнопку для открытия мини-приложения.",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		log.Printf("Ошибка при отправке сообщения: %v", err)
	}
}

// startWebServer запускает HTTP-сервер с маршрутизацией
func startWebServer(botToken string) {
	http.HandleFunc("/", serveIndex)
	// Обработчик для перехода с нативной кнопки
	http.HandleFunc("/api/open", func(rw http.ResponseWriter, req *http.Request) {
		handlerAPIOpen(rw, req, botToken)
	})

	port := "8080"
	log.Printf("Веб-сервер запущен на порту :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// serveIndex обслуживает главную страницу index.html
func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

// handlerAPIOpen проверяет параметры запроса от миниаппа
func handlerAPIOpen(rw http.ResponseWriter, req *http.Request, botToken string) {
	// Валидируем параметры веб-приложения
	user, ok := bot.ValidateWebappRequest(req.URL.Query(), botToken)
	if !ok {
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
	}

	log.Printf("Пользователь: %+v", user)
	rw.Write([]byte("Данные получены!"))
}
