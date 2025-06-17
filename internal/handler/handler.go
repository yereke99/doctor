// Updated handler.go to work with your existing repository structure

package handler

import (
	"context"
	"doctor/config"
	"doctor/internal/repository"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/go-telegram/ui/slider"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type Handler struct {
	repo                    *repository.DoctorRepository
	userRepo                *repository.UserRepository
	redisRepo               *repository.RedisRepository
	specialtyMapping        map[string]string
	reverseSpecialtyMapping map[string]string
	logger                  *zap.Logger
	cfg                     *config.Config
}

// NewHandler инициализирует Handler с репозиториями
func NewHandler(
	repo *repository.DoctorRepository,
	userRepo *repository.UserRepository,
	redisRepo *repository.RedisRepository,
	logger *zap.Logger,
	cfg *config.Config,
) *Handler {
	return &Handler{
		repo:      repo,
		userRepo:  userRepo,
		redisRepo: redisRepo,
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
		cfg:    cfg,
	}
}

func (h *Handler) SendHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.From.ID != h.cfg.AdminID {
		return
	}

	// Определяем тип сообщения и получаем fileID/caption
	msgType, fileID, caption := h.parseMessage(msg)

	// Загружаем всех пользователей
	userIDs, err := []int64{}, errors.New("new error test")
	if err != nil {
		h.logger.Error("failed to load user IDs", zap.Error(err))
		return
	}

	rateLimiter := rate.NewLimiter(rate.Every(time.Second/30), 1)
	var successCount, failCount int64
	errGroup, ctx := errgroup.WithContext(ctx)

	for _, userID := range userIDs {
		errGroup.Go(func() error {
			if err := rateLimiter.Wait(ctx); err != nil {
				return err
			}
			if err := h.sendToUser(ctx, b, userID, msgType, fileID, caption); err != nil {
				atomic.AddInt64(&failCount, 1)
				h.logger.Error("send failed", zap.Error(err), zap.Int64("chatID", userID))
			} else {
				atomic.AddInt64(&successCount, 1)
			}
			return nil
		})
	}
	// Итоговый отчёт для админа
	summary := fmt.Sprintf(
		"📣 Рассылка завершена:\n✅ Успешно: %d\n❌ Ошибок: %d",
		atomic.LoadInt64(&successCount),
		atomic.LoadInt64(&failCount),
	)
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   summary,
	})
}

func (h *Handler) parseMessage(msg *models.Message) (msgType, fileID, caption string) {
	switch {
	case msg.Text != "":
		return "text", "", msg.Text
	case len(msg.Photo) > 0:
		return "photo", msg.Photo[len(msg.Photo)-1].FileID, msg.Caption
	case msg.Video != nil:
		return "video", msg.Video.FileID, msg.Caption
	case msg.Document != nil:
		return "document", msg.Document.FileID, msg.Caption
	case msg.Caption != "":
		return "caption", "", msg.Caption
	default:
		return "", "", ""
	}
}

// sendToUser отправляет одному пользователю указанное сообщение
func (h *Handler) sendToUser(
	ctx context.Context,
	b *bot.Bot,
	chatID int64,
	msgType, fileID, caption string,
) error {
	switch msgType {
	case "text":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: caption})
		return err
	case "photo":
		_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   &models.InputFileString{Data: fileID},
			Caption: caption,
		})
		return err
	case "video":
		_, err := b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:  chatID,
			Video:   &models.InputFileString{Data: fileID},
			Caption: caption,
		})
		return err
	case "document":
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:   chatID,
			Document: &models.InputFileString{Data: fileID},
			Caption:  caption,
		})
		return err
	default:
		return nil
	}
}

// DefaultHandler отвечает на любые текстовые сообщения.
func (h *Handler) DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	// Инлайн-кнопка с эмодзи и ссылкой
	markup := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{
				Text: "📲 Открыть MediHub",
				URL:  "https://t.me/dariger_test_bot/mediHub",
			},
		}},
	}

	// Текст с эмодзи
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        "Загляните в MediHub по кнопке ниже 👇",
		ReplyMarkup: markup,
	})
	if err != nil {
		h.logger.Error("error in send message default handler", zap.Error(err))
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

	// Обновляем статус подтверждения
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

// DoctorHandler handles doctor registration
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

	h.logger.Info("Doctor found", zap.Any("doctor", doctor))

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
	if doctor.DiplomaPath != nil && *doctor.DiplomaPath != "" {
		response["diploma_url"] = fmt.Sprintf("/files/documents/%s", filepath.Base(*doctor.DiplomaPath))
	}
	if doctor.CertPath != nil && *doctor.CertPath != "" {
		response["certificate_url"] = fmt.Sprintf("/files/documents/%s", filepath.Base(*doctor.CertPath))
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

	// Create directories if not exists
	avaDir := "./ava"
	docsDir := "./documents"
	if err := os.MkdirAll(avaDir, 0755); err != nil {
		h.logger.Error("Failed to create avatar directory", zap.Error(err))
	}
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		h.logger.Error("Failed to create documents directory", zap.Error(err))
	}

	// Handle file uploads
	var avatarPath, diplomaPath, certPath *string

	// Handle avatar upload
	if file, header, err := r.FormFile("avatar"); err == nil {
		defer file.Close()

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

	// Handle diploma upload
	if file, header, err := r.FormFile("diploma"); err == nil {
		defer file.Close()

		// Generate unique filename
		filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), header.Filename)
		fullPath := filepath.Join(docsDir, filename)

		// Save file
		out, err := os.Create(fullPath)
		if err != nil {
			h.logger.Error("Failed to create diploma file", zap.Error(err))
			http.Error(w, "Failed to save diploma", http.StatusInternalServerError)
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			h.logger.Error("Failed to copy diploma data", zap.Error(err))
			http.Error(w, "Failed to save diploma", http.StatusInternalServerError)
			return
		}

		diplomaPath = &fullPath
	}

	// Handle certificate upload
	if file, header, err := r.FormFile("certificate"); err == nil {
		defer file.Close()

		// Generate unique filename
		filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), header.Filename)
		fullPath := filepath.Join(docsDir, filename)

		// Save file
		out, err := os.Create(fullPath)
		if err != nil {
			h.logger.Error("Failed to create certificate file", zap.Error(err))
			http.Error(w, "Failed to save certificate", http.StatusInternalServerError)
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			h.logger.Error("Failed to copy certificate data", zap.Error(err))
			http.Error(w, "Failed to save certificate", http.StatusInternalServerError)
			return
		}

		certPath = &fullPath
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

	// Only update files if new ones were uploaded
	if avatarPath != nil {
		updateDoc.AvatarPath = avatarPath
	}
	if diplomaPath != nil {
		updateDoc.DiplomaPath = diplomaPath
	}
	if certPath != nil {
		updateDoc.CertPath = certPath
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
	if updatedDoctor.DiplomaPath != nil && *updatedDoctor.DiplomaPath != "" {
		response["diploma_url"] = fmt.Sprintf("/files/documents/%s", filepath.Base(*updatedDoctor.DiplomaPath))
	}
	if updatedDoctor.CertPath != nil && *updatedDoctor.CertPath != "" {
		response["certificate_url"] = fmt.Sprintf("/files/documents/%s", filepath.Base(*updatedDoctor.CertPath))
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

// GetUserStatusHandler handles GET requests to check user status
func (h *Handler) GetUserStatusHandler(w http.ResponseWriter, r *http.Request) {
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

	// Extract user ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	userIDStr := pathParts[3]
	userTelegramID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID format", http.StatusBadRequest)
		return
	}

	h.logger.Info("Getting user status", zap.Int64("telegram_id", userTelegramID))

	// Get user status using the doctor repository as checker
	status, err := h.userRepo.GetUserStatus(userTelegramID, h.repo)
	if err != nil {
		h.logger.Error("Error getting user status", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("User status retrieved",
		zap.Int64("telegram_id", userTelegramID),
		zap.Bool("is_doctor", status.IsDoctor),
		zap.Bool("is_client", status.IsClient),
		zap.Bool("doctor_agreement", status.DoctorAgreementAccepted),
		zap.Bool("patient_agreement", status.PatientAgreementAccepted))

	// Send JSON response
	if err := json.NewEncoder(w).Encode(status); err != nil {
		h.logger.Error("Error encoding JSON response", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// SaveUserAgreementHandler handles POST requests to save user agreement
func (h *Handler) SaveUserAgreementHandler(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse JSON body
	var requestData struct {
		TelegramID        int64  `json:"telegram_id"`
		UserType          string `json:"user_type"`
		AgreementAccepted bool   `json:"agreement_accepted"`
		Timestamp         string `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if requestData.TelegramID == 0 || requestData.UserType == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Validate user type
	if requestData.UserType != "doctor" && requestData.UserType != "patient" {
		http.Error(w, "Invalid user type", http.StatusBadRequest)
		return
	}

	// Save agreement to database
	if err := h.userRepo.SaveUserAgreement(requestData.TelegramID, requestData.UserType, requestData.AgreementAccepted); err != nil {
		h.logger.Error("Error saving user agreement", zap.Error(err))
		http.Error(w, "Failed to save agreement", http.StatusInternalServerError)
		return
	}

	h.logger.Info("User agreement saved",
		zap.Int64("telegram_id", requestData.TelegramID),
		zap.String("user_type", requestData.UserType),
		zap.Bool("accepted", requestData.AgreementAccepted))

	// Return success response
	response := map[string]interface{}{
		"success": true,
		"message": "Agreement saved successfully",
		"data": map[string]interface{}{
			"telegram_id": requestData.TelegramID,
			"user_type":   requestData.UserType,
			"accepted":    requestData.AgreementAccepted,
			"timestamp":   time.Now(),
		},
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Error encoding JSON response", zap.Error(err))
	}
}

// PatientAppointmentHandler handles patient appointment requests
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

	// Parse user ID
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

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
		// Save client registration to user repository
		clientReg := &repository.ClientRegistration{
			UserID:      userID,
			Fio:         fullName,
			Sex:         gender,
			Problem:     complaints,
			Period:      duration,
			MedPersonal: rawSpecialty,
			Contact:     contacts,
			Address:     address,
			Time:        time.Now().Format("2006-01-02 15:04:05"),
		}

		// Check if client already exists, update or insert
		exists, err := h.userRepo.ClientExists(userID)
		if err != nil {
			h.logger.Error("Error checking client existence", zap.Error(err))
		}

		if exists {
			if err := h.userRepo.UpdateClient(clientReg); err != nil {
				h.logger.Error("Error updating client", zap.Error(err))
			}
		} else {
			if err := h.userRepo.InsertClient(clientReg); err != nil {
				h.logger.Error("Error inserting client", zap.Error(err))
			}
		}

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

			var msgID int
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
					msgID = msg.ID
				} else {
					h.logger.Warn("Ошибка отправки врачу", zap.Error(err))
					continue
				}
			} else {
				msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID:      doc.TelegramID,
					Text:        msgText,
					ReplyMarkup: markup,
				})
				if err == nil {
					msgID = msg.ID
				} else {
					h.logger.Warn("Ошибка отправки врачу", zap.Error(err))
					continue
				}
			}

			docMsg := repository.DocMsg{
				ChatID: doc.TelegramID,
				MsgID:  msgID,
			}

			if err := h.redisRepo.AddDocMsg(userID, docMsg); err != nil {
				h.logger.Error("Ошибка сохранения в Redis", zap.Error(err), zap.Int64("userID", userID))
			}
		}

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

	msgs, err := h.redisRepo.GetDocMsgs(userID)
	if err != nil {
		h.logger.Error("Ошибка получения сообщений из Redis", zap.Error(err), zap.Int64("userID", userID))
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка обработки",
		})
		return
	}

	for _, dm := range msgs {
		if dm.ChatID != docChatID {
			if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
				ChatID:    dm.ChatID,
				MessageID: dm.MsgID,
			}); err != nil {
				h.logger.Warn("Ошибка удаления сообщения",
					zap.Error(err),
					zap.Int64("chatID", dm.ChatID),
					zap.Int("msgID", dm.MsgID))
			}
		}
	}

	if err := h.redisRepo.DeleteDocMsgs(userID); err != nil {
		h.logger.Error("Ошибка удаления из Redis", zap.Error(err), zap.Int64("userID", userID))
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Ошибка обработки",
		})
		return
	}

	// удаляем собственное приглашение
	/*
		if mq := update.CallbackQuery.Message; mq.Message != nil {
				b.DeleteMessage(ctx, &bot.DeleteMessageParams{
					ChatID:    mq.Message.Chat.ID,
					MessageID: mq.Message.ID,
				})
			}
	*/

	// убираем spinner
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Қабылдадым!",
	})

	// уведомляем врача
	/*
		b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: update.CallbackQuery.From.ID,
				Text:   "Хабарлама сәтті жойылды!",
			})
	*/

	// уведомляем пациента
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: userID,
		Text:   "Сіздің өтінішіңіз қабылданды, дәрігер жақын арада хабарласатын болады.",
	})

	doc, err := h.repo.GetDoctorByTelegramID(update.CallbackQuery.From.ID)
	if err != nil {
		h.logger.Error("Ошибка получения данных доктора", zap.Error(err))
	} else {
		var f *os.File
		photoPath := *doc.AvatarPath
		caption := fmt.Sprintf("Ваш врач: %s\nКонтакт: %s", *doc.FullName, *doc.Contact)
		if photoPath != "" {
			f, err = os.Open(photoPath)
			if err != nil {
				h.logger.Error("error in open doctor ava", zap.Error(err))
			}
		}
		if _, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID: userID,
			Photo: &models.InputFileUpload{
				Filename: photoPath,
				Data:     f,
			}, // путь к файлу ./ava/…
			Caption: caption,
		}); err != nil {
			h.logger.Error("Ошибка отправки фото доктора пациенту",
				zap.Error(err),
				zap.String("photoPath", photoPath),
			)
		}
	}
}

// StartWebServer starts the HTTP server with all routes
func (h *Handler) StartWebServer(botToken string, ctx context.Context, b *bot.Bot) {
	// User status and agreement routes
	http.HandleFunc("/api/user/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agreement") {
			// Handle agreement endpoint
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.WriteHeader(http.StatusOK)
				return
			}
			h.SaveUserAgreementHandler(w, r)
		} else {
			// Handle user status endpoint
			h.GetUserStatusHandler(w, r)
		}
	})

	// Separate agreement endpoint for clarity
	http.HandleFunc("/api/user/agreement", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.SaveUserAgreementHandler(w, r)
	})

	// Doctor routes
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

	// Patient appointment route
	http.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		h.PatientAppointmentHandler(w, r, ctx, b)
	})

	// Serve static files (avatars and documents)
	fileServer := http.FileServer(http.Dir("."))
	http.Handle("/files/", http.StripPrefix("/files/", fileServer))

	// Serve PDF files from offerta directory
	http.Handle("/offerta/", http.StripPrefix("/offerta/", http.FileServer(http.Dir("./offerta/"))))

	// Welcome  page route
	http.HandleFunc("/welcome", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/index.html"
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Debug file not found", zap.String("path", templatePath))
			http.Error(w, "Debug page not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, templatePath)
	})

	// Mini app routes
	http.HandleFunc("/update-doctor", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/update-doctor.html"
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Template file not found", zap.String("path", templatePath))
			http.Error(w, "Template not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, templatePath)
	})

	http.HandleFunc("/client", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/client.html"
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Template file not found", zap.String("path", templatePath))
			http.Error(w, "Template not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, templatePath)
	})

	http.HandleFunc("/doctors", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/doctor.html"
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Template file not found", zap.String("path", templatePath))
			http.Error(w, "Template not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, templatePath)
	})

	// Debug test page route
	http.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		debugPath := "./debug.html"
		if _, err := os.Stat(debugPath); os.IsNotExist(err) {
			h.logger.Error("Debug file not found", zap.String("path", debugPath))
			http.Error(w, "Debug page not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, debugPath)
	})

	// Main index route - serve the new index.html
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		// Serve the main index.html file
		indexPath := "./index.html"
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			h.logger.Error("Index file not found", zap.String("path", indexPath))
			http.Error(w, "Index not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, indexPath)
	})

	addr := ":8080"
	h.logger.Info("веб-сервер запущен", zap.String("addr", addr))
	log.Fatal(http.ListenAndServe(addr, nil))
}

// Close closes all repository connections
func (h *Handler) Close() error {
	if h.userRepo != nil {
		if err := h.userRepo.Close(); err != nil {
			h.logger.Error("Error closing user repository", zap.Error(err))
			return err
		}
	}
	return nil
}
