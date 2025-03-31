package main

import (
	"context"
	"doctor/config"
	"doctor/traits/logger"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

// PatientData представляет данные, отправляемые с мини-приложения
type PatientData struct {
	FullName   string `json:"fullName"`
	Age        string `json:"age"`
	Gender     string `json:"gender"`
	Complaints string `json:"complaints"`
	Duration   string `json:"duration"`
	Specialty  string `json:"specialty"`
	Contacts   string `json:"contacts"`
	Address    string `json:"address"`
	UserID     string `json:"user_id"`
	InitData   string `json:"initData"`
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

	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
	}

	token := cfg.Token

	b, err := bot.New(token, opts...)
	if err != nil {
		zapLogger.Error("error creating bot config", zap.Error(err))
		return
	}

	// Запуск веб-сервера для обработки запросов мини-приложения
	go startWebServer(cfg.Token)
	zapLogger.Info("started bot")
	b.Start(ctx)
}

func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	// Формируем URL для открытия мини-приложения
	webAppURL := "https://ba2c1bcf6cb9b3282b29ce19c2090862.serveo.net/doctor"
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

func startWebServer(botToken string) {
	http.HandleFunc("/", serveIndex)
	// Обработчик для получения данных от мини-приложения
	http.HandleFunc("/api/open", func(rw http.ResponseWriter, req *http.Request) {
		handlerAPIOpen(rw, req, botToken)
	})

	port := "8080"
	log.Printf("Веб-сервер запущен на порту :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	// Отдаем файл index.html
	http.ServeFile(w, r, "index.html")
}

func handlerAPIOpen(rw http.ResponseWriter, req *http.Request, botToken string) {
	// Получаем параметры запроса
	query := req.URL.Query()
	// Проводим валидацию с помощью Telegram SDK
	user, ok := bot.ValidateWebappRequest(query, botToken)
	if !ok {
		http.Error(rw, "unauthorized", http.StatusUnauthorized)
		return
	}

	fmt.Println("Полученные данные от мини-приложения:")
	for key, values := range query {
		fmt.Printf("%s: %v\n", key, values)
	}
	log.Printf("Пользователь: %+v", user)
	rw.Write([]byte("Данные получены!"))
}
