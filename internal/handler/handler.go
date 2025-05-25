// handler/handler.go
package handler

import (
	"context"
	"doctor/internal/domain"
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

type docMsg struct {
	chatID int64
	msgID  int
}

type Handler struct {
	registrations           map[int64]domain.DoctorRegistration
	doctorsBySpecialty      map[string][]domain.DoctorRegistration
	patientRequests         map[int64][]docMsg // userID → список сообщений врачам
	patientReqMu            sync.Mutex
	regMu                   sync.Mutex
	specialtyMapping        map[string]string
	reverseSpecialtyMapping map[string]string
	logger                  *zap.Logger
}

// NewHandler инициализирует Handler с пустыми хранилищами и картами специальностей.
func NewHandler(logger *zap.Logger) *Handler {
	return &Handler{
		registrations:      make(map[int64]domain.DoctorRegistration),
		doctorsBySpecialty: make(map[string][]domain.DoctorRegistration),
		patientRequests:    make(map[int64][]docMsg),
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

func (h *Handler) DoctorHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	// CORS
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

	// Парсим форму
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
		return
	}
	fullName := r.FormValue("full_name")
	specialty := r.FormValue("specialty")
	contact := r.FormValue("contact")
	tid, err := strconv.ParseInt(r.FormValue("telegram_id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный Telegram ID: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Отправляем мгновенный ответ, чтобы фронт не ждал
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))

	// Фон: сохраняем файлы и шлём слайдер администраторам
	go func() {
		avaDir, docsDir := "./ava", "./documents"
		os.MkdirAll(avaDir, 0755)
		os.MkdirAll(docsDir, 0755)

		type saveResult struct {
			label string
			path  string
			err   error
		}
		files := []struct {
			field, label, dst string
		}{
			{"avatar", "Аватарка", avaDir},
			{"diploma", "Диплом", docsDir},
			{"certificate", "Сертификат", docsDir},
		}

		var wg sync.WaitGroup
		results := make(chan saveResult, len(files))

		for _, f := range files {
			wg.Add(1)
			go func(field, label, dst string) {
				defer wg.Done()
				file, hdr, ferr := r.FormFile(field)
				if ferr != nil {
					results <- saveResult{label, "", ferr}
					return
				}
				defer file.Close()
				name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), hdr.Filename)
				path := filepath.Join(dst, name)
				out, err := os.Create(path)
				if err != nil {
					results <- saveResult{label, "", err}
					return
				}
				defer out.Close()

				if _, err := io.Copy(out, file); err != nil {
					results <- saveResult{label, "", err}
					return
				}
				results <- saveResult{label, path, nil}
			}(f.field, f.label, f.dst)
		}

		wg.Wait()
		close(results)

		// собираем только успешные файлы
		var slides []slider.Slide
		for res := range results {
			if res.err != nil {
				h.logger.Warn("Ошибка сохранения файла", zap.String("file", res.label), zap.Error(res.err))
				continue
			}
			data, err := os.ReadFile(res.path)
			if err != nil {
				h.logger.Warn("Ошибка чтения сохранённого файла", zap.String("path", res.path), zap.Error(err))
				continue
			}
			slides = append(slides, slider.Slide{
				Text:     res.label,
				Photo:    string(data),
				IsUpload: true,
			})
		}

		// Регистрируем врача в памяти
		docID := time.Now().Unix()
		reg := domain.DoctorRegistration{
			ID:         docID,
			FullName:   fullName,
			Specialty:  specialty,
			Contact:    contact,
			TelegramID: tid,
			AvatarPath: "", DiplomaPath: "", CertPath: "",
		}
		// Из результатов извлечём пути
		for _, s := range slides {
			switch s.Text {
			case "Профиль фотосы":
				reg.AvatarPath = s.Photo
			case "Диплом":
				reg.DiplomaPath = s.Photo
			case "Сертификат":
				reg.CertPath = s.Photo
			}
		}

		h.regMu.Lock()
		h.registrations[docID] = reg
		h.doctorsBySpecialty[specialty] = append(h.doctorsBySpecialty[specialty], reg)
		h.regMu.Unlock()

		// Отправляем слайдер администраторам
		onSelect := func(ctx context.Context, b *bot.Bot, msg models.MaybeInaccessibleMessage, idx int) {
			if msg.Message != nil {
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: msg.Message.Chat.ID,
					Text:   fmt.Sprintf("Регистрация врача %s подтверждена ✅", reg.FullName),
				})
			}
		}
		opts := []slider.Option{slider.OnSelect("✅ Қабылдау", true, onSelect)}
		for _, admin := range []int64{800703982} {
			sl := slider.New(b, slides, opts...)
			sl.Show(ctx, b, admin)
		}

		// Уведомляем самого врача
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: tid,
			Text:   "Ваша заявка отправлена на рассмотрение. Ожидайте подтверждения.",
		})
	}()
}

// PatientAppointmentHandler обрабатывает заявки от пациентов (GET/POST).
func (h *Handler) PatientAppointmentHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	// CORS & быстрый ответ
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Парсим форму
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Ошибка парсинга формы: "+err.Error(), http.StatusBadRequest)
		return
	}
	userIDStr := r.FormValue("user_id")
	fullName := r.FormValue("full_name")
	age := r.FormValue("age")
	gender := r.FormValue("gender")
	complaints := r.FormValue("complaints")
	duration := r.FormValue("duration")
	rawSpecialty := r.FormValue("specialty")
	contacts := r.FormValue("contacts")
	address := r.FormValue("address")

	// читаем фото жалобы
	var photoData []byte
	var photoName string
	if file, hdr, ferr := r.FormFile("complaint_photo"); ferr == nil {
		defer file.Close()
		if data, err := io.ReadAll(file); err == nil {
			photoData = data
			photoName = hdr.Filename
		}
	}

	// сразу возвращаем OK, чтобы клиент не ждал тяжёлой работы
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))

	go func() {
		// 1) сохраняем фото
		var photoPath, fileName string
		if len(photoData) > 0 {
			dir := "./patient"
			if err := os.MkdirAll(dir, 0755); err != nil {
				h.logger.Error("error in create directory", zap.Error(err))
			}
			fn := fmt.Sprintf("patient_%d_%s", time.Now().UnixNano(), photoName)
			path := filepath.Join(dir, fn)
			if err := os.WriteFile(path, photoData, 0644); err != nil {
				h.logger.Warn("Ошибка сохранения фото", zap.Error(err))
			}
			photoPath, fileName = path, fn
		}

		photoPath, fileName = "", ""

		// 2) готовим текст сообщения
		dispSpec := rawSpecialty
		if rev, ok := h.reverseSpecialtyMapping[rawSpecialty]; ok {
			dispSpec = rev
		}
		msgText := fmt.Sprintf(
			"Новая заявка:\n"+
				"ФИО: %s\nВозраст: %s\nПол: %s\nЖалобы: %s\nДлительность: %s дн.\n"+
				"Специальность: %s\nКонтакты: %s\nАдрес: %s",
			fullName, age, gender, complaints, duration,
			dispSpec, contacts, address,
		)

		// 3) рассылаем врачам и сохраняем msgID
		userID, _ := strconv.ParseInt(userIDStr, 10, 64)
		var sent []docMsg
		f, err := os.Open(photoPath)
		if err != nil {
			h.logger.Warn("Ошибка открытия файла", zap.Error(err))
			return
		}
		defer f.Close()
		for _, doc := range h.doctorsBySpecialty[rawSpecialty] {
			cb := fmt.Sprintf("delete_%d_%d", userID, doc.TelegramID)
			markup := &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{{
					{Text: "✅ Қабылдадым", CallbackData: cb},
				}},
			}

			if photoPath != "" && fileName != "" {
				msg, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
					ChatID: doc.TelegramID,
					Photo: &models.InputFileUpload{
						Filename: fileName,
						Data:     f,
					},
					Caption:     msgText,
					ReplyMarkup: markup,
				})
				if err == nil {
					sent = append(sent, docMsg{chatID: doc.TelegramID, msgID: msg.ID})
				} else {
					h.logger.Warn("Ошибка отправки врачу", zap.Error(err))
				}
			} else {
				msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:      doc.TelegramID,
					Text:        msgText,
					ReplyMarkup: markup,
				})
				if err == nil {
					sent = append(sent, docMsg{chatID: doc.TelegramID, msgID: msg.ID})
				} else {
					h.logger.Warn("Ошибка отправки врачу", zap.Error(err))
				}
			}

		}

		// сохраняем для DeleteMessageHandler
		h.patientReqMu.Lock()
		h.patientRequests[userID] = sent
		h.patientReqMu.Unlock()

		// 4) отправляем в общий чат
		groupID := int64(-1009876543210)
		b.SendMessage(ctx, &bot.SendMessageParams{ChatID: groupID, Text: msgText})
	}()
}

// DeleteMessageHandler — удаляет заявки у других врачей при первом нажатии
func (h *Handler) DeleteMessageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	parts := strings.Split(update.CallbackQuery.Data, "_")
	if len(parts) != 3 {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Неверные данные",
		})
		return
	}
	userID, err1 := strconv.ParseInt(parts[1], 10, 64)
	docChatID, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Неверные данные",
		})
		return
	}

	// получаем и удаляем список сообщений
	h.patientReqMu.Lock()
	msgs := h.patientRequests[userID]
	delete(h.patientRequests, userID)
	h.patientReqMu.Unlock()

	// удаляем у всех, кроме того, кто нажал
	for _, dm := range msgs {
		if dm.chatID != docChatID {
			b.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    dm.chatID,
				MessageID: dm.msgID,
			})
		}
	}

	// удаляем собственное приглашение
	if mq := update.CallbackQuery.Message; mq.Message != nil {
		b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    mq.Message.Chat.ID,
			MessageID: mq.Message.ID,
		})
	}

	// убираем spinner
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Қабылдадым!",
	})

	// уведомляем врача
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.CallbackQuery.From.ID,
		Text:   "Хабарлама сәтті жойылды!",
	})
	// уведомляем пациента
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: userID,
		Text:   "Сіздің өтінішіңіз қабылданды, дәрігер жақын арада хабарласатын болады.",
	})
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
