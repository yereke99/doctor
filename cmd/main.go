package main

import (
	"bytes"
	"context"
	"doctor/config"
	"doctor/traits/logger"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
)

// DoctorRegistration хранит данные регистрации врача.
type DoctorRegistration struct {
	ID         int64
	FullName   string
	DoctorType string
	Experience string
	WorkDate   string
	StartTime  string
	EndTime    string
	TelegramID int64  // Telegram ID доктора (передаётся из формы)
	FilePath   string // Путь сохранённого файла
}

var (
	registrations      = make(map[int64]DoctorRegistration)    // общая мапа регистрации врача
	doctorsBySpecialty = make(map[string][]DoctorRegistration) // для каждой унифицированной специальности срез врачей
	regMu              sync.Mutex
)

// specialtyMapping приводит названия специальностей (на русском) к унифицированному виду (на английском).
var specialtyMapping = map[string]string{
	"Терапевт":          "THERAPIST",
	"Хирург":            "SURGEON",
	"Кардиолог":         "CARDIOLOG",
	"Невролог":          "NEUROLOGIST",
	"ЛОР":               "ENT",
	"Психолог":          "PSYCHOLOGIST",
	"Врач на дому":      "HOME_DOCTOR",
	"Медсестра на дому": "HOME_NURSE",
	"Анализ":            "LAB_TEST",
	"Капельница к медперсоналу": "IV_DRIP",
}

// reverseSpecialtyMapping возвращает удобочитаемый вид специальности.
var reverseSpecialtyMapping = map[string]string{
	"THERAPIST":    "Терапевт",
	"SURGEON":      "Хирург",
	"CARDIOLOG":    "Кардиолог",
	"NEUROLOGIST":  "Невролог",
	"ENT":          "ЛОР",
	"PSYCHOLOGIST": "Психолог",
	"HOME_DOCTOR":  "Врач на дому",
	"HOME_NURSE":   "Медсестра на дому",
	"LAB_TEST":     "Анализ",
	"IV_DRIP":      "Капельница к медперсоналу",
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

	// Регистрируем стандартный хендлер и обработчики inline callback
	opts := []bot.Option{
		bot.WithDefaultHandler(handler),
		bot.WithCallbackQueryDataHandler("doctor_", bot.MatchTypePrefix, InlineHandlerWrapper),
	}

	token := cfg.Token

	b, err := bot.New(token, opts...)
	if err != nil {
		zapLogger.Error("error creating bot config", zap.Error(err))
		return
	}

	// Запускаем веб-сервер с двумя маршрутами:
	// /doctor — регистрация врача (POST-форма с файлом)
	// /api/open — приём заявок от пациентов (GET-запрос с query-параметрами)
	go startWebServer(cfg.Token, ctx, b)
	zapLogger.Info("started bot")
	b.Start(ctx)
}

// handler — стандартный обработчик входящих сообщений (например, если пользователь пишет боту напрямую).
func handler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Пожалуйста, используйте веб-форму для регистрации.",
	})
	if err != nil {
		log.Printf("Ошибка отправки сообщения: %v", err)
	}
}

// InlineHandler обрабатывает нажатия inline-кнопок для подтверждения заявки врача.
// Ожидается, что callback.Data имеет формат "doctor_{id}".
func InlineHandler(ctx context.Context, b *bot.Bot, callback *models.CallbackQuery) {
	parts := strings.Split(callback.Data, "_")
	if len(parts) < 2 {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Неверные данные",
		})
		return
	}

	doctorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Ошибка обработки ID",
		})
		return
	}

	regMu.Lock()
	registration, exists := registrations[doctorID]
	if exists {
		delete(registrations, doctorID)
	}
	regMu.Unlock()

	if !exists {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Регистрация не найдена",
		})
		return
	}

	confirmationText := "Ваша регистрация подтверждена. Вы теперь доктор! 😊"
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: registration.TelegramID,
		Text:   confirmationText,
	})
	if err != nil {
		log.Printf("Ошибка отправки сообщения доктору: %v", err)
	}

	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
		Text:            "Доктор подтвержден!",
	})
}

// InlineHandlerWrapper адаптирует InlineHandler к типу bot.HandlerFunc.
func InlineHandlerWrapper(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	InlineHandler(ctx, b, update.CallbackQuery)
}

// startWebServer регистрирует маршруты и запускает HTTP-сервер.
func startWebServer(botToken string, ctx context.Context, b *bot.Bot) {
	// Обработчик для регистрации врача.
	http.HandleFunc("/doctor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			serveDoctor(w, r)
		} else if r.Method == http.MethodPost {
			doctorHandler(w, r, ctx, b)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Обработчик для заявок от пациентов.
	http.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			patientAppointmentHandler(w, r, ctx, b)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Дополнительный маршрут для статики.
	http.HandleFunc("/", serveIndex)

	port := "8080"
	log.Printf("Веб-сервер запущен на порту :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

func serveDoctor(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "doctor.html")
}

// doctorHandler обрабатывает POST-запрос с данными регистрации врача.
// Данные сохраняются, файл записывается на диск, и отправляется уведомление администраторам с документом.
func doctorHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
		return
	}

	fullName := r.FormValue("full_name")
	doctorType := r.FormValue("doctor_type")
	experience := r.FormValue("experience")
	workDate := r.FormValue("work_date")
	startTime := r.FormValue("start_time")
	endTime := r.FormValue("end_time")
	telegramIDStr := r.FormValue("telegram_id")
	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Неверный Telegram ID: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("document")
	if err != nil {
		http.Error(w, "Ошибка получения файла: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	docDir := "./documents"
	if err := os.MkdirAll(docDir, 0755); err != nil {
		http.Error(w, "Ошибка создания директории: "+err.Error(), http.StatusInternalServerError)
		return
	}

	filePath := filepath.Join(docDir, header.Filename)
	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Ошибка создания файла: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Ошибка сохранения файла: "+err.Error(), http.StatusInternalServerError)
		return
	}

	doctorID := time.Now().Unix()
	registration := DoctorRegistration{
		ID:         doctorID,
		FullName:   fullName,
		DoctorType: doctorType,
		Experience: experience,
		WorkDate:   workDate,
		StartTime:  startTime,
		EndTime:    endTime,
		TelegramID: telegramID,
		FilePath:   filePath,
	}

	// Сохраняем регистрацию в общей мапе и по специальности.
	regMu.Lock()
	registrations[doctorID] = registration
	// Определяем ключ: если имеется унифицированное название, то используем его.
	key := doctorType
	if norm, ok := specialtyMapping[doctorType]; ok {
		key = norm
	}
	doctorsBySpecialty[key] = append(doctorsBySpecialty[key], registration)
	regMu.Unlock()

	// Формируем текст для администратора с полными данными.
	caption := fmt.Sprintf(
		"Регистрация врача:\nФИО: %s\nСпециализация: %s\nСтаж: %s\nДата: %s\nВремя: %s - %s",
		fullName, doctorType, experience, workDate, startTime, endTime,
	)

	// Инлайн-клавиатура для подтверждения заявки.
	inlineKeyboard := models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         "Подтвердить",
					CallbackData: fmt.Sprintf("doctor_%d", doctorID),
				},
			},
		},
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Ошибка чтения файла: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Отправляем документ администраторам.
	admins := []int{800703982, 809550522}
	for _, admin := range admins {
		fileReader := bytes.NewReader(data)
		adminDoc := &bot.SendDocumentParams{
			ChatID: admin,
			Document: &models.InputFileUpload{
				Filename: filepath.Base(filePath),
				Data:     fileReader,
			},
			Caption:     caption,
			ParseMode:   "HTML",
			ReplyMarkup: inlineKeyboard,
		}
		_, err := b.SendDocument(ctx, adminDoc)
		if err != nil {
			log.Printf("Ошибка отправки документа админу (ID %d): %v", admin, err)
		}
	}

	// Сообщаем доктору, что заявка отправлена, и даём ссылку для перехода к боту.
	doctorMsg := fmt.Sprintf(
		"Ваша заявка отправлена. Перейдите по ссылке для дальнейших инструкций: %s\nОжидайте ответа от модератора.",
		"https://t.me/dariger_test_bot",
	)
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: registration.TelegramID,
		Text:   doctorMsg,
	})
	if err != nil {
		log.Printf("Ошибка отправки сообщения доктору: %v", err)
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Данные получены и документ отправлен администраторам на подтверждение!"))
}

// patientAppointmentHandler обрабатывает GET-запросы от пациентов с заявками на приём.
// Сначала формируется полное сообщение с контактами, которое отправляется глобальным администраторам.
// Затем, если для выбранной специальности есть зарегистрированные врачи (из doctorsBySpecialty),
// отправляется уведомление этим врачам (без контактов, адреса и Telegram ID),
// а специальность выводится в удобочитаемом виде (например, «Кардиолог»).
func patientAppointmentHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	q := r.URL.Query()
	fullName := q.Get("full_name")
	age := q.Get("age")
	gender := q.Get("gender")
	complaints := q.Get("complaints")
	duration := q.Get("duration")
	specialty := q.Get("specialty")
	contacts := q.Get("contacts")
	address := q.Get("address")

	// Если ФИО пустое, подставляем значение "Не указан".
	if fullName == "" {
		fullName = "Не указан"
	}

	// Приводим специальность к унифицированному виду, если она есть в specialtyMapping.
	if mapped, ok := specialtyMapping[specialty]; ok {
		specialty = mapped
	}
	// Преобразуем специальность обратно для вывода врачу (удобочитаемо).
	specialtyHuman := specialty
	if rev, ok := reverseSpecialtyMapping[specialty]; ok {
		specialtyHuman = rev
	}

	// Формируем полное сообщение для глобальных администраторов (без Telegram ID).
	fullMsgText := fmt.Sprintf(
		"Новая заявка на приём:\nФИО: %s\nВозраст: %s\nПол: %s\nЖалобы: %s\nДлительность симптомов: %s дней\nСпециальность: %s\nКонтакты: %s\nАдрес: %s",
		fullName, age, gender, complaints, duration, specialtyHuman, contacts, address,
	)
	admins := []int{800703982, 809550522}
	for _, admin := range admins {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: admin,
			Text:   fullMsgText,
		})
		if err != nil {
			log.Printf("Ошибка отправки сообщения админу (ID %d): %v", admin, err)
		}
	}

	// Если для выбранной специальности имеются зарегистрированные врачи,
	// отправляем им уведомление (без контактов, адреса и Telegram ID).
	doctors, ok := doctorsBySpecialty[specialty]
	if ok && len(doctors) > 0 {
		msgForDoctors := fmt.Sprintf(
			"Новая заявка на приём:\nФИО: %s\nВозраст: %s\nПол: %s\nЖалобы: %s\nДлительность симптомов: %s дней\nСпециальность: %s",
			fullName, age, gender, complaints, duration, specialtyHuman,
		)
		for _, doc := range doctors {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: doc.TelegramID,
				Text:   msgForDoctors,
			})
			if err != nil {
				log.Printf("Ошибка отправки сообщения доктору (ID %d): %v", doc.TelegramID, err)
			}
		}
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Данные получены и заявка отправлена администраторам!"))
}
