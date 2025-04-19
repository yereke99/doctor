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
	"github.com/go-telegram/ui/slider"
	"go.uber.org/zap"
)

// Обновлённая структура регистрации
type DoctorRegistration struct {
	ID          int64
	FullName    string
	Contact     string
	TelegramID  int64
	AvatarPath  string // будет сохраняться в ./ava
	DiplomaPath string // в ./documents
	CertPath    string // в ./documents
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

	// Запускаем веб-сервер с маршрутами:
	// /doctor — регистрация врача (POST-форма с файлом)
	// /api/open — приём заявок от пациентов (GET/POST-запрос)
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

func startWebServer(botToken string, ctx context.Context, b *bot.Bot) {
	// Обработчик для регистрации врача.
	http.HandleFunc("/doctor", func(w http.ResponseWriter, r *http.Request) {
		// CORS-preflight
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodPost {
			doctorHandler(w, r, ctx, b)
		} else {
			http.Error(w, "Метод не разрешён", http.StatusMethodNotAllowed)
		}
	})

	// Обработчик для заявок от пациентов.
	http.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodGet {
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

// Обработчик /doctor POST с использованием slider для отправки трёх файлов
func doctorHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	// CORS‑preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
		return
	}

	fullName := r.FormValue("full_name")
	contact := r.FormValue("contact")
	telegramIDStr := r.FormValue("telegram_id")
	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Неверный Telegram ID: "+err.Error(), http.StatusBadRequest)
		return
	}

	avaDir := "./ava"
	docsDir := "./documents"
	os.MkdirAll(avaDir, 0755)
	os.MkdirAll(docsDir, 0755)

	saveFile := func(field, dst string) (string, error) {
		file, hdr, err := r.FormFile(field)
		if err != nil {
			return "", err
		}
		defer file.Close()
		name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), hdr.Filename)
		path := filepath.Join(dst, name)
		out, err := os.Create(path)
		if err != nil {
			return "", err
		}
		defer out.Close()
		if _, err := io.Copy(out, file); err != nil {
			return "", err
		}
		return path, nil
	}

	avatarPath, err := saveFile("avatar", avaDir)
	if err != nil {
		http.Error(w, "Ошибка сохранения аватарки: "+err.Error(), http.StatusBadRequest)
		return
	}
	diplomaPath, err := saveFile("diploma", docsDir)
	if err != nil {
		http.Error(w, "Ошибка сохранения диплома: "+err.Error(), http.StatusBadRequest)
		return
	}
	certPath, err := saveFile("certificate", docsDir)
	if err != nil {
		http.Error(w, "Ошибка сохранения сертификата: "+err.Error(), http.StatusBadRequest)
		return
	}

	doctorID := time.Now().Unix()
	reg := DoctorRegistration{
		ID:          doctorID,
		FullName:    fullName,
		Contact:     contact,
		TelegramID:  telegramID,
		AvatarPath:  avatarPath,
		DiplomaPath: diplomaPath,
		CertPath:    certPath,
	}
	regMu.Lock()
	registrations[doctorID] = reg
	regMu.Unlock()

	// Формируем слайды: аватар, диплом, сертификат
	files := []struct{ Label, Path string }{
		{"Аватарка", reg.AvatarPath},
		{"Диплом", reg.DiplomaPath},
		{"Сертификат", reg.CertPath},
	}
	slides := make([]slider.Slide, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			log.Printf("Ошибка чтения файла %s: %v", f.Path, err)
			continue
		}
		slides = append(slides, slider.Slide{
			Text:     f.Label,
			Photo:    string(data),
			IsUpload: true,
		})
	}

	// Callback при нажатии «Подтвердить»
	onSelect := func(ctx context.Context, b *bot.Bot, msg models.MaybeInaccessibleMessage, idx int) {
		if msg.Message != nil {
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: msg.Message.Chat.ID,
				Text:   fmt.Sprintf("Регистрация врача %s подтверждена ✅", reg.FullName),
			})
		}
	}

	opts := []slider.Option{
		slider.OnSelect("✅ Подтвердить", true, onSelect),
	}

	admins := []int64{800703982, 809550522}
	for _, adminID := range admins {
		sl := slider.New(b, slides, opts...)
		sl.Show(ctx, b, adminID)
	}

	// Уведомляем самого врача
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: reg.TelegramID,
		Text:   "Ваша заявка отправлена на рассмотрение. Ожидайте подтверждения.",
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Регистрация принята и файлы сохранены."))
}

// patientAppointmentHandler обрабатывает заявки от пациентов с данными и фото жалобы.
// Если фото жалобы передано, оно сохраняется в директорию "./patient".
func patientAppointmentHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	var (
		fullName, age, gender, complaints, duration string
		specialty, contacts, address                string
		photoData                                   []byte
		photoFileName                               string
		err                                         error
	)

	// Поддержка POST (с файлом) и GET (без файла)
	if r.Method == http.MethodPost {
		if err = r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
			return
		}
		fullName = r.FormValue("full_name")
		age = r.FormValue("age")
		gender = r.FormValue("gender")
		complaints = r.FormValue("complaints")
		duration = r.FormValue("duration")
		specialty = r.FormValue("specialty")
		contacts = r.FormValue("contacts")
		address = r.FormValue("address")
		file, header, err := r.FormFile("complaint_photo")
		if err == nil { // файл найден
			defer file.Close()
			photoData, err = io.ReadAll(file)
			if err != nil {
				photoData = nil
			} else {
				photoFileName = header.Filename
			}
		} else {
			photoData = nil
		}
	} else { // GET-запрос
		q := r.URL.Query()
		fullName = q.Get("full_name")
		age = q.Get("age")
		gender = q.Get("gender")
		complaints = q.Get("complaints")
		duration = q.Get("duration")
		specialty = q.Get("specialty")
		contacts = q.Get("contacts")
		address = q.Get("address")
		photoData = nil
	}

	// Сохранение фото жалобы в директорию "./patient" (если файл передан)
	if photoData != nil {
		patientDir := "./patient"
		if err := os.MkdirAll(patientDir, 0755); err != nil {
			log.Printf("Ошибка создания директории '%s': %v", patientDir, err)
		} else {
			fileName := fmt.Sprintf("patient_%d_%s", time.Now().UnixNano(), photoFileName)
			savePath := filepath.Join(patientDir, fileName)
			if err := os.WriteFile(savePath, photoData, 0644); err != nil {
				log.Printf("Ошибка сохранения файла в '%s': %v", patientDir, err)
			} else {
				log.Printf("Фото жалобы успешно сохранено: %s", savePath)
			}
		}
	}

	// Приводим специальность к унифицированному виду.
	if mapped, ok := specialtyMapping[specialty]; ok {
		specialty = mapped
	}
	// Получаем удобочитаемый вид специальности.
	specialtyHuman := specialty
	if rev, ok := reverseSpecialtyMapping[specialty]; ok {
		specialtyHuman = rev
	}

	// Формируем два варианта сообщения:
	// Полное сообщение для администраторов (с контактами и адресом)
	fullMsgText := fmt.Sprintf(
		"Новая заявка на приём:\nФИО: %s\nВозраст: %s\nПол: %s\nЖалобы: %s\nДлительность симптомов: %s дней\nСпециальность: %s\nКонтакты: %s\nАдрес: %s",
		fullName, age, gender, complaints, duration, specialtyHuman, contacts, address,
	)
	// Уменьшённое сообщение для врачей и группы (без контактов и адреса)
	reducedMsgText := fmt.Sprintf(
		"Новая заявка на приём:\nФИО: %s\nВозраст: %s\nПол: %s\nЖалобы: %s\nДлительность симптомов: %s дней\nСпециальность: %s",
		fullName, age, gender, complaints, duration, specialtyHuman,
	)

	// Список администраторов и группа
	admins := []int{800703982, 809550522}
	var groupID int64 = -1009876543210

	// Функция отправки сообщения (с фото, если оно есть)
	sendMsg := func(chatID int64, text string) {
		if photoData != nil {
			photoUpload := &models.InputFileUpload{
				Filename: photoFileName,
				Data:     bytes.NewReader(photoData),
			}
			_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
				ChatID:  chatID,
				Photo:   photoUpload,
				Caption: text,
			})
			if err != nil {
				log.Printf("Ошибка отправки фото в чат (ID %d): %v", chatID, err)
			}
		} else {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: chatID,
				Text:   text,
			})
			if err != nil {
				log.Printf("Ошибка отправки сообщения в чат (ID %d): %v", chatID, err)
			}
		}
	}

	// Рассылка заявки администраторам (полное сообщение).
	for _, admin := range admins {
		sendMsg(int64(admin), fullMsgText)
	}

	// Рассылка заявки врачам по выбранной специальности (уменьшённое сообщение).
	doctors, ok := doctorsBySpecialty[specialty]
	if ok && len(doctors) > 0 {
		for _, doc := range doctors {
			sendMsg(doc.TelegramID, reducedMsgText)
		}
	}

	// Рассылка заявки в группу (уменьшённое сообщение).
	sendMsg(groupID, reducedMsgText)

	// Рассылка заявки в канал/чат @mediHubDoctors.
	if photoData != nil {
		photoUpload := &models.InputFileUpload{
			Filename: photoFileName,
			Data:     bytes.NewReader(photoData),
		}
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  "@mediHubDoctors",
			Photo:   photoUpload,
			Caption: fullMsgText,
		})
		if err != nil {
			log.Printf("Ошибка отправки фото в чат @mediHubDoctors: %v", err)
		}
	} else {
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: "@mediHubDoctors",
			Text:   fullMsgText,
		})
		if err != nil {
			log.Printf("Ошибка отправки сообщения в чат @mediHubDoctors: %v", err)
		}
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Данные получены и заявка отправлена администраторам (полностью), врачам и группе (без контактов и адреса), а также в @mediHubDoctors!"))
}
