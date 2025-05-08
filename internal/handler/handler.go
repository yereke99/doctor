// handler/handler.go
package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

// DoctorRegistration хранит данные регистрации врача.
type DoctorRegistration struct {
	ID          int64
	FullName    string
	Contact     string
	TelegramID  int64
	AvatarPath  string
	DiplomaPath string
	CertPath    string
}

// Handler содержит все данные и методы для обработки запросов и callback.
type Handler struct {
	registrations           map[int64]DoctorRegistration
	doctorsBySpecialty      map[string][]DoctorRegistration
	regMu                   sync.Mutex
	specialtyMapping        map[string]string
	reverseSpecialtyMapping map[string]string
	logger                  *zap.Logger
}

// NewHandler инициализирует Handler с пустыми хранилищами и картами специальностей.
func NewHandler(logger *zap.Logger) *Handler {
	return &Handler{
		registrations:      make(map[int64]DoctorRegistration),
		doctorsBySpecialty: make(map[string][]DoctorRegistration),
		specialtyMapping: map[string]string{
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
		},
		reverseSpecialtyMapping: map[string]string{
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
		},
		logger: logger,
	}
}

// DefaultHandler отвечает на любые текстовые сообщения.
func (h *Handler) DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Пожалуйста, используйте веб-форму для регистрации.",
	})
	if err != nil {
		h.logger.Warn("ошибка отправки сообщения DefaultHandler", zap.Error(err))
	}
}

// InlineHandler обрабатывает нажатия по кнопке подтверждения регистрации врача.
func (h *Handler) InlineHandler(ctx context.Context, b *bot.Bot, callback *models.CallbackQuery) {
	parts := strings.Split(callback.Data, "_")
	if len(parts) < 2 {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Неверные данные"})
		return
	}
	doctorID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Ошибка обработки ID"})
		return
	}
	h.regMu.Lock()
	reg, ok := h.registrations[doctorID]
	if ok {
		delete(h.registrations, doctorID)
	}
	h.regMu.Unlock()
	if !ok {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Регистрация не найдена"})
		return
	}
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: reg.TelegramID, Text: "Ваша регистрация подтверждена. Вы теперь доктор! 😊"})
	if err != nil {
		h.logger.Warn("Ошибка отправки подтверждения доктору", zap.Error(err))
	}
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Доктор подтвержден!"})
}

// InlineHandlerWrapper адаптирует InlineHandler к Signature bot.HandlerFunc.
func (h *Handler) InlineHandlerWrapper(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}
	h.InlineHandler(ctx, b, update.CallbackQuery)
}

// DoctorHandler обрабатывает POST-форму /doctor для регистрации врача.
func (h *Handler) DoctorHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не разрешён", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
		return
	}
	fullName := r.FormValue("full_name")
	contact := r.FormValue("contact")
	tid, err := strconv.ParseInt(r.FormValue("telegram_id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный Telegram ID: "+err.Error(), http.StatusBadRequest)
		return
	}
	avaDir, docsDir := "./ava", "./documents"
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
	diplomaPath, err2 := saveFile("diploma", docsDir)
	certPath, err3 := saveFile("certificate", docsDir)
	if err != nil || err2 != nil || err3 != nil {
		http.Error(w, "Ошибка сохранения файлов", http.StatusBadRequest)
		return
	}
	docID := time.Now().Unix()
	reg := DoctorRegistration{ID: docID, FullName: fullName, Contact: contact, TelegramID: tid, AvatarPath: avatarPath, DiplomaPath: diplomaPath, CertPath: certPath}
	h.regMu.Lock()
	h.registrations[docID] = reg
	h.regMu.Unlock()
	files := []struct{ Label, Path string }{{"Аватарка", avatarPath}, {"Диплом", diplomaPath}, {"Сертификат", certPath}}
	var slides []slider.Slide
	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			h.logger.Warn("чтение файла", zap.String("path", f.Path), zap.Error(err))
			continue
		}
		slides = append(slides, slider.Slide{Text: f.Label, Photo: string(data), IsUpload: true})
	}
	onSelect := func(ctx context.Context, b *bot.Bot, msg models.MaybeInaccessibleMessage, idx int) {
		if msg.Message != nil {
			b.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Message.Chat.ID, Text: fmt.Sprintf("Регистрация врача %s подтверждена ✅", reg.FullName)})
		}
	}
	opts := []slider.Option{slider.OnSelect("✅ Подтвердить", true, onSelect)}
	admins := []int64{800703982, 809550522}
	for _, admin := range admins {
		sl := slider.New(b, slides, opts...)
		sl.Show(ctx, b, admin)
	}
	b.SendMessage(ctx, &bot.SendMessageParams{ChatID: tid, Text: "Ваша заявка отправлена на рассмотрение. Ожидайте подтверждения."})
	w.Write([]byte("Регистрация принята и файлы сохранены."))
}

// PatientAppointmentHandler обрабатывает заявки от пациентов (GET/POST).
func (h *Handler) PatientAppointmentHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	// CORS-preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

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
		file, header, ferr := r.FormFile("complaint_photo")
		if ferr == nil {
			defer file.Close()
			photoData, err = io.ReadAll(file)
			if err == nil {
				photoFileName = header.Filename
			}
		}
	} else {
		// GET-запрос
		q := r.URL.Query()
		fullName = q.Get("full_name")
		age = q.Get("age")
		gender = q.Get("gender")
		complaints = q.Get("complaints")
		duration = q.Get("duration")
		specialty = q.Get("specialty")
		contacts = q.Get("contacts")
		address = q.Get("address")
	}
	// Сохранение фото жалобы, если есть
	if len(photoData) > 0 {
		patientDir := "./patient"
		if mkerr := os.MkdirAll(patientDir, 0755); mkerr != nil {
			h.logger.Warn("Ошибка создания директории для фото", zap.Error(mkerr))
		}
		fileName := fmt.Sprintf("patient_%d_%s", time.Now().UnixNano(), photoFileName)
		savePath := filepath.Join(patientDir, fileName)
		if werr := os.WriteFile(savePath, photoData, 0644); werr != nil {
			h.logger.Warn("Ошибка сохранения фото жалобы", zap.Error(werr))
		}
	}
	// Унификация специальности
	if mapped, ok := h.specialtyMapping[specialty]; ok {
		specialty = mapped
	}
	// Читаемый вид специальности
	specialtyHuman := specialty
	if rev, ok := h.reverseSpecialtyMapping[specialty]; ok {
		specialtyHuman = rev
	}
	// Формируем тексты сообщений
	fullMsgText := fmt.Sprintf(
		`Новая заявка на приём:
		ФИО: %s
		Возраст: %s
		Пол: %s
		Жалобы: %s
		Длительность симптомов: %s дней
		Специальность: %s
		Контакты: %s
		Адрес: %s`,
		fullName, age, gender, complaints, duration, specialtyHuman, contacts, address,
	)
	reducedMsgText := fmt.Sprintf(
		`Новая заявка на приём:
		ФИО: %s
		Возраст: %s
		Пол: %s
		Жалобы: %s
		Длительность симптомов: %s дней
		Специальность: %s`,
		fullName, age, gender, complaints, duration, specialtyHuman,
	)
	// Список админов и группы
	admins := []int64{800703982, 809550522}
	var groupID int64 = -1009876543210
	// Функция отправки сообщений
	sendMsg := func(chatID int64, text string) {
		if len(photoData) > 0 {
			photoUpload := &models.InputFileUpload{Filename: photoFileName, Data: bytes.NewReader(photoData)}
			if _, err := b.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: photoUpload, Caption: text}); err != nil {
				h.logger.Warn("Ошибка отправки фото пациентской заявки", zap.Int64("chatID", chatID), zap.Error(err))
			}
		} else {
			if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text}); err != nil {
				h.logger.Warn("Ошибка отправки текстового сообщения пациентской заявки", zap.Int64("chatID", chatID), zap.Error(err))
			}
		}
	}
	// Отправляем администраторам (полное сообщение)
	for _, admin := range admins {
		sendMsg(admin, fullMsgText)
	}
	// Отправляем врачам по специальности (сокращённое сообщение)
	if docs, ok := h.doctorsBySpecialty[specialty]; ok {
		for _, doc := range docs {
			sendMsg(doc.TelegramID, reducedMsgText)
		}
	}
	// Отправляем группе
	sendMsg(groupID, reducedMsgText)
	// Отправляем в канал @mediHubDoctors
	if len(photoData) > 0 {
		photoUpload := &models.InputFileUpload{Filename: photoFileName, Data: bytes.NewReader(photoData)}
		if _, err = b.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: "@mediHubDoctors", Photo: photoUpload, Caption: fullMsgText}); err != nil {
			h.logger.Warn("Ошибка отправки фото в @mediHubDoctors", zap.Error(err))
		}
	} else {
		if _, err = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: "@mediHubDoctors", Text: fullMsgText}); err != nil {
			h.logger.Warn("Ошибка отправки сообщения в @mediHubDoctors", zap.Error(err))
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Данные получены и заявка отправлена."))
}

// StartWebServer запускает HTTP сервер с маршрутами /doctor и /api/open.
func (h *Handler) StartWebServer(botToken string, ctx context.Context, b *bot.Bot) {
	http.HandleFunc("/doctor", func(w http.ResponseWriter, r *http.Request) {
		h.DoctorHandler(w, r, ctx, b)
	})
	http.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		h.PatientAppointmentHandler(w, r, ctx, b)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	addr := ":8080"
	h.logger.Info("веб-сервер запущен", zap.String("addr", addr))
	log.Fatal(http.ListenAndServe(addr, nil))
}
