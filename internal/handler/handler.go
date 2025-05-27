// handler/handler.go
package handler

import (
	"context"
	"doctor/internal/repository"
	"encoding/json"
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
	repo                    *repository.DoctorRepository
	patientRequests         map[int64][]docMsg // userID → список сообщений врачам
	patientReqMu            sync.Mutex
	specialtyMapping        map[string]string
	reverseSpecialtyMapping map[string]string
	logger                  *zap.Logger
}

// NewHandler инициализирует Handler с репозиторием и картами специальностей.
func NewHandler(repo *repository.DoctorRepository, logger *zap.Logger) *Handler {
	return &Handler{
		repo:            repo,
		patientRequests: make(map[int64][]docMsg),
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
	doctorTelegramID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Ошибка обработки ID"})
		return
	}

	// Проверяем, есть ли доктор в БД
	exists, err := h.repo.CheckDoctor(doctorTelegramID)
	if err != nil {
		h.logger.Error("Ошибка проверки доктора", zap.Error(err))
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Ошибка базы данных"})
		return
	}
	if !exists {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callback.ID, Text: "Регистрация не найдена"})
		return
	}

	// Обновляем статус подтверждения (если у вас есть такое поле в БД)
	// Для простоты пока просто отправляем уведомление
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: doctorTelegramID,
		Text:   "Ваша регистрация подтверждена. Вы теперь доктор! 😊",
	})
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

	// Проверяем, не зарегистрирован ли уже доктор
	exists, err := h.repo.CheckDoctor(tid)
	if err != nil {
		h.logger.Error("Ошибка проверки доктора", zap.Error(err))
		http.Error(w, "Ошибка базы данных", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "Доктор уже зарегистрирован", http.StatusConflict)
		return
	}

	// Отправляем мгновенный ответ
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))

	// Фон: сохраняем файлы и регистрируем доктора
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
		savedPaths := make(map[string]string)

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

		// Собираем успешные файлы и пути
		var slides []slider.Slide
		for res := range results {
			if res.err != nil {
				h.logger.Warn("Ошибка сохранения файла", zap.String("file", res.label), zap.Error(res.err))
				continue
			}
			// Сохраняем пути
			switch res.label {
			case "Аватарка":
				savedPaths["avatar"] = res.path
			case "Диплом":
				savedPaths["diploma"] = res.path
			case "Сертификат":
				savedPaths["certificate"] = res.path
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

		// Сохраняем доктора в БД
		now := time.Now()
		avaPath := savedPaths["avatar"]
		diplomaPath := savedPaths["diploma"]
		certPath := savedPaths["certificate"]

		doc := &repository.DoctorRegistration{
			TelegramID:       tid,
			FullName:         &fullName,
			TypeOfSpecialist: &specialty,
			Contact:          &contact,
			AvatarPath:       &avaPath,
			DiplomaPath:      &diplomaPath,
			CertPath:         &certPath,
			Time:             &now,
		}

		if err := h.repo.Insert(doc); err != nil {
			h.logger.Error("Ошибка сохранения доктора в БД", zap.Error(err))
			// Уведомляем врача об ошибке
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: tid,
				Text:   "Произошла ошибка при сохранении ваших данных. Пожалуйста, попробуйте снова.",
			})
			return
		}

		// Отправляем слайдер администраторам для подтверждения
		onSelect := func(ctx context.Context, b *bot.Bot, msg models.MaybeInaccessibleMessage, idx int) {
			if msg.Message != nil {
				b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: msg.Message.Chat.ID,
					Text:   fmt.Sprintf("Регистрация врача %s подтверждена ✅", fullName),
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

// PatientAppointmentHandler обрабатывает заявки от пациентов
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

	// возвращаем OK
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
			} else {
				photoPath, fileName = path, fn
			}
		}

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

		// 3) получаем докторов по специальности из БД
		doctors, err := h.repo.GetDoctorsBySpecialty(rawSpecialty)
		if err != nil {
			h.logger.Error("Ошибка получения докторов", zap.Error(err))
			return
		}

		// 4) рассылаем врачам
		userID, _ := strconv.ParseInt(userIDStr, 10, 64)
		var sent []docMsg
		var f *os.File
		if photoPath != "" {
			f, err = os.Open(photoPath)
			if err != nil {
				h.logger.Warn("Ошибка открытия файла", zap.Error(err))
			} else {
				defer f.Close()
			}
		}

		for _, doc := range doctors {
			cb := fmt.Sprintf("delete_%d_%d", userID, doc.TelegramID)
			markup := &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{{
					{Text: "✅ Қабылдадым", CallbackData: cb},
				}},
			}

			if f != nil {
				f.Seek(0, 0) // Перематываем файл в начало для каждой отправки
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

		// 5) отправляем в общий чат
		groupID := int64(-1009876543210)
		b.SendMessage(ctx, &bot.SendMessageParams{ChatID: groupID, Text: msgText})
	}()
}

// DeleteMessageHandler удаляет заявки у других врачей при первом нажатии
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

// GetDoctorHandler handles GET requests to fetch doctor data
func (h *Handler) GetDoctorHandler(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract doctor ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid doctor ID", http.StatusBadRequest)
		return
	}

	doctorIDStr := pathParts[2]
	doctorTelegramID, err := strconv.ParseInt(doctorIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid doctor ID format", http.StatusBadRequest)
		return
	}

	// Get doctor from DB
	doctor, err := h.repo.GetDoctorByTelegramID(doctorTelegramID)
	if err != nil {
		h.logger.Error("Ошибка получения доктора", zap.Error(err))
		http.Error(w, "Doctor not found", http.StatusNotFound)
		return
	}

	fmt.Println("Doctor found: ", doctor)

	// Prepare response
	response := map[string]interface{}{
		"id":          doctor.ID,
		"telegram_id": doctor.TelegramID,
	}

	// Add non-nil fields
	if doctor.FullName != nil {
		response["full_name"] = *doctor.FullName
	}
	if doctor.TypeOfSpecialist != nil {
		response["specialty"] = *doctor.TypeOfSpecialist
	}
	if doctor.Contact != nil {
		response["contact"] = *doctor.Contact
	}
	if doctor.AvatarPath != nil && *doctor.AvatarPath != "" {
		response["avatar_url"] = fmt.Sprintf("/files/ava/%s", filepath.Base(*doctor.AvatarPath))
	}

	// Send JSON response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Error encoding JSON response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// UpdateDoctorHandler handles PUT requests to update doctor data
func (h *Handler) UpdateDoctorHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Error parsing form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Extract form values
	telegramIDStr := r.FormValue("telegram_id")
	fullName := r.FormValue("full_name")
	specialty := r.FormValue("specialty")
	contact := r.FormValue("contact")

	// Validate telegram ID
	telegramID, err := strconv.ParseInt(telegramIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid telegram ID", http.StatusBadRequest)
		return
	}

	// Check if doctor exists
	exists, err := h.repo.CheckDoctor(telegramID)
	if err != nil {
		h.logger.Error("Ошибка проверки доктора", zap.Error(err))
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Doctor not found", http.StatusNotFound)
		return
	}

	// Validate required fields
	if fullName == "" || specialty == "" || contact == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Handle avatar upload if provided
	var avatarPath *string
	if file, header, err := r.FormFile("avatar"); err == nil {
		defer file.Close()

		// Create avatar directory if not exists
		avaDir := "./ava"
		if err := os.MkdirAll(avaDir, 0755); err != nil {
			h.logger.Error("Failed to create avatar directory", zap.Error(err))
			http.Error(w, "Failed to save avatar", http.StatusInternalServerError)
			return
		}

		// Generate unique filename
		filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), header.Filename)
		fullPath := filepath.Join(avaDir, filename)

		// Save file
		out, err := os.Create(fullPath)
		if err != nil {
			h.logger.Error("Failed to create avatar file", zap.Error(err))
			http.Error(w, "Failed to save avatar", http.StatusInternalServerError)
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			h.logger.Error("Failed to copy avatar data", zap.Error(err))
			http.Error(w, "Failed to save avatar", http.StatusInternalServerError)
			return
		}

		avatarPath = &fullPath
	}

	// Prepare update
	now := time.Now()
	updateDoc := &repository.DoctorRegistration{
		TelegramID:       telegramID,
		FullName:         &fullName,
		TypeOfSpecialist: &specialty,
		Contact:          &contact,
		Time:             &now,
	}

	// Only update avatar if new one was uploaded
	if avatarPath != nil {
		updateDoc.AvatarPath = avatarPath
	}

	// Update in DB
	if err := h.repo.UpdateDoctor(updateDoc); err != nil {
		h.logger.Error("Ошибка обновления доктора", zap.Error(err))
		http.Error(w, "Failed to update doctor", http.StatusInternalServerError)
		return
	}

	// Get updated doctor data
	updatedDoctor, err := h.repo.GetDoctorByTelegramID(telegramID)
	if err != nil {
		h.logger.Error("Ошибка получения обновленного доктора", zap.Error(err))
		http.Error(w, "Failed to retrieve updated data", http.StatusInternalServerError)
		return
	}

	// Prepare response
	response := map[string]interface{}{
		"id":          updatedDoctor.ID,
		"telegram_id": updatedDoctor.TelegramID,
	}

	if updatedDoctor.FullName != nil {
		response["full_name"] = *updatedDoctor.FullName
	}
	if updatedDoctor.TypeOfSpecialist != nil {
		response["specialty"] = *updatedDoctor.TypeOfSpecialist
	}
	if updatedDoctor.Contact != nil {
		response["contact"] = *updatedDoctor.Contact
	}
	if updatedDoctor.AvatarPath != nil && *updatedDoctor.AvatarPath != "" {
		response["avatar_url"] = fmt.Sprintf("/files/ava/%s", filepath.Base(*updatedDoctor.AvatarPath))
	}

	// Send success notification to doctor
	go func() {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: telegramID,
			Text:   "✅ Ваши данные успешно обновлены!",
		})
		if err != nil {
			h.logger.Warn("Error sending update notification", zap.Error(err))
		}
	}()

	// Send JSON response
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Error encoding JSON response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// StartWebServer starts the HTTP server with all routes
func (h *Handler) StartWebServer(botToken string, ctx context.Context, b *bot.Bot) {
	// Existing routes
	http.HandleFunc("/doctor", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			h.DoctorHandler(w, r, ctx, b)
		case http.MethodPut:
			h.UpdateDoctorHandler(w, r, ctx, b)
		case http.MethodOptions:
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, PUT, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Route for getting doctor data
	http.HandleFunc("/doctor/", func(w http.ResponseWriter, r *http.Request) {
		h.GetDoctorHandler(w, r)
	})

	http.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		h.PatientAppointmentHandler(w, r, ctx, b)
	})

	// Serve static files (avatars and documents)
	fileServer := http.FileServer(http.Dir("."))
	http.Handle("/files/", http.StripPrefix("/files/", fileServer))

	// Serve the update mini app
	http.HandleFunc("/update-doctor", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./server/templates/update-doctor.html")
	})

	http.HandleFunc("/client", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./server/templates/client.html")
	})

	http.HandleFunc("/doctors", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./server/templates/doctor.html")
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	addr := ":8080"
	h.logger.Info("веб-сервер запущен", zap.String("addr", addr))
	log.Fatal(http.ListenAndServe(addr, nil))
}
