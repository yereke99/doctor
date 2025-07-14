package handler

import (
	"context"
	"doctor/config"
	"doctor/internal/domain"
	"doctor/internal/repository"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	adminScreenChats        map[int64]int64 // adminID -> userID mapping for screening chats
	screenChatMutex         sync.RWMutex
}

// ScreeningResult struct to match the frontend data
type ScreeningResult struct {
	UserID           int64                  `json:"user_id"`
	Timestamp        string                 `json:"timestamp"`
	Language         string                 `json:"language"`
	BMI              *float64               `json:"bmi,omitempty"`
	Answers          map[string]interface{} `json:"answers"`
	FormattedMessage string                 `json:"formatted_message"` // Pre-formatted message from frontend
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
		logger:           logger,
		cfg:              cfg,
		adminScreenChats: make(map[int64]int64),
	}
}

// Handle screening chat between admin and user
func (h *Handler) handleScreenChat(ctx context.Context, b *bot.Bot, update *models.Update) {
	adminID := update.Message.From.ID

	h.screenChatMutex.RLock()
	userID, exists := h.adminScreenChats[adminID]
	h.screenChatMutex.RUnlock()

	if !exists {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminID,
			Text:   "❌ Активті чат табылмады.",
		})
		if err != nil {
			h.logger.Error("error sending no active chat message", zap.Error(err))
		}
		return
	}

	// Handle exit command
	if update.Message.Text == "/exit" || strings.Contains(update.Message.Text, "exit_chat") {
		h.exitScreenChat(ctx, b, adminID)
		return
	}

	// Forward admin's message to user with protection
	var err error

	switch {
	case update.Message.Text != "":
		// Remove ParseMode to avoid markdown parsing errors
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:         userID,
			Text:           update.Message.Text,
			ProtectContent: true,
			// Removed ParseMode: models.ParseModeMarkdown to fix the error
		})

	case len(update.Message.Photo) > 0:
		caption := update.Message.Caption
		_, err = b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:         userID,
			Photo:          &models.InputFileString{Data: update.Message.Photo[len(update.Message.Photo)-1].FileID},
			Caption:        caption,
			ProtectContent: true,
		})

	case update.Message.Video != nil:
		caption := update.Message.Caption
		_, err = b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:         userID,
			Video:          &models.InputFileString{Data: update.Message.Video.FileID},
			Caption:        caption,
			ProtectContent: true,
		})

	case update.Message.VideoNote != nil:
		_, err = b.SendVideoNote(ctx, &bot.SendVideoNoteParams{
			ChatID:         userID,
			VideoNote:      &models.InputFileString{Data: update.Message.VideoNote.FileID},
			ProtectContent: true,
		})

	case update.Message.Voice != nil:
		_, err = b.SendVoice(ctx, &bot.SendVoiceParams{
			ChatID:         userID,
			Voice:          &models.InputFileString{Data: update.Message.Voice.FileID},
			ProtectContent: true,
		})

	case update.Message.Audio != nil:
		caption := update.Message.Caption
		_, err = b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:         userID,
			Audio:          &models.InputFileString{Data: update.Message.Audio.FileID},
			Caption:        caption,
			ProtectContent: true,
		})

	case update.Message.Document != nil:
		caption := update.Message.Caption
		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:         userID,
			Document:       &models.InputFileString{Data: update.Message.Document.FileID},
			Caption:        caption,
			ProtectContent: true,
		})
	}

	if err != nil {
		h.logger.Error("error forwarding message to user", 
			zap.Error(err),
			zap.Int64("admin_id", adminID),
			zap.Int64("user_id", userID),
			zap.String("message_text", update.Message.Text))
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminID,
			Text:   "❌ Хабарламаны жіберу кезінде қате орын алды.",
		})
	} else {
		// Send confirmation to admin with exit button
		markup := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{
				{Text: "🚪 Чатты аяқтау", CallbackData: "exit_chat"},
			}},
		}

		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      adminID,
			Text:        "✅ Хабарлама жіберілді",
			ReplyMarkup: markup,
		})
	}
}

// Alternative version with proper markdown escaping if you want to keep markdown
func escapeMarkdownV2(text string) string {
	// Characters that need to be escaped in MarkdownV2
	specialChars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	
	result := text
	for _, char := range specialChars {
		result = strings.ReplaceAll(result, char, "\\"+char)
	}
	return result
}


// Exit screening chat
func (h *Handler) exitScreenChat(ctx context.Context, b *bot.Bot, adminID int64) {
	h.screenChatMutex.Lock()
	userID, exists := h.adminScreenChats[adminID]
	if exists {
		delete(h.adminScreenChats, adminID)
	}
	h.screenChatMutex.Unlock()

	if exists {
		// Save state to Redis for persistence
		h.redisRepo.DeleteUserState(ctx, userID) // Clean up user state

		// Notify admin
		userState := h.getOrCreateUserState(ctx, adminID)
		userState.State = stateAdminPanel
		h.redisRepo.SaveUserState(ctx, adminID, userState)

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminID,
			Text:   "🚪 Чат аяқталды. Админ панеліне оралдыңыз.",
		})
		if err != nil {
			h.logger.Error("error sending chat exit message", zap.Error(err))
		}
        // Notify admin
		// Notify admin
		markup := &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{{
				{Text: "Медициналық жаңалықтар", URL: "https://t.me/chek_izi"},
			}},
		}
		// Notify user
		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: userID,
			Text:   "🏥 Дәрігермен кеңесу аяқталды. Қосымша сұрақтарыңыз болса, қайта хабарласыңыз.",
			ReplyMarkup: markup,
		})
		if err != nil {
			h.logger.Error("error sending chat exit message to user", zap.Error(err))
		}
	}
}

// Handle screening inline button responses
func (h *Handler) InlineScreenAnswer(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.CallbackQuery == nil {
		return
	}

	callback := update.CallbackQuery

	// Handle exit_chat callback
	if callback.Data == "exit_chat" {
		h.exitScreenChat(ctx, b, callback.From.ID)
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Чат аяқталды",
		})
		return
	}

	// Parse screen_userId callback
	parts := strings.Split(callback.Data, "_")
	if len(parts) != 2 || parts[0] != "screen" {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Қате деректер",
		})
		return
	}

	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            "Қате пайдаланушы ID",
		})
		return
	}

	adminID := callback.From.ID

	// Check if admin already has an active chat
	h.screenChatMutex.RLock()
	existingUserID, hasActiveChat := h.adminScreenChats[adminID]
	h.screenChatMutex.RUnlock()

	if hasActiveChat {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callback.ID,
			Text:            fmt.Sprintf("Сізде %d пайдаланушысымен белсенді чат бар", existingUserID),
		})
		return
	}

	// Start new screening chat
	h.screenChatMutex.Lock()
	h.adminScreenChats[adminID] = userID
	h.screenChatMutex.Unlock()

	// Save state to Redis
	adminState := h.getOrCreateUserState(ctx, adminID)
	adminState.State = stateScreenChat
	h.redisRepo.SaveUserState(ctx, adminID, adminState)

	// Notify admin
	markup := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "🚪 Чатты аяқтау", CallbackData: "exit_chat"},
		}},
	}

	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      adminID,
		Text:        fmt.Sprintf("💬 %d пайдаланушысымен чат басталды.\n\nХабарлама жіберіңіз:", userID),
		ReplyMarkup: markup,
	})
	if err != nil {
		h.logger.Error("error starting screening chat", zap.Error(err))
	}

	// Notify user
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: userID,
		Text:   "👨‍⚕️ Дәрігер сізбен байланысқа шықты. Скрининг нәтижелері бойынша кеңес беріледі.",
	})
	if err != nil {
		h.logger.Error("error notifying user about chat start", zap.Error(err))
	}

	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
		Text:            "Чат басталды",
	})
}

func (h *Handler) SendHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.From.ID != h.cfg.AdminID {
		return
	}

	// Определяем тип сообщения и получаем fileID/caption
	msgType, fileID, caption := h.parseMessage(msg)

	// Загружаем всех пользователей
	userIDs, err := []int64{}, errors.New("nil")
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

// graceful degradation for Redis failures
func (h *Handler) getOrCreateUserState(ctx context.Context, userID int64) *domain.UserState {
	state, err := h.redisRepo.GetUserState(ctx, userID)
	if err != nil {
		h.logger.Error("Redis error, using fallback state",
			zap.Error(err),
			zap.Int64("user_id", userID))

		// Return a safe default state
		return &domain.UserState{
			State:  stateStart,
			Count:  0,
			IsPaid: false,
		}
	}

	if state == nil {
		state = &domain.UserState{
			State:  stateStart,
			Count:  0,
			IsPaid: false,
		}

		// Try to save, but don't fail if Redis is down
		if err := h.redisRepo.SaveUserState(ctx, userID, state); err != nil {
			h.logger.Warn("Failed to save state to Redis, continuing with in-memory state",
				zap.Error(err))
		}
	}

	return state
}

// DefaultHandler отвечает на любые текстовые сообщения.
func (h *Handler) DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	userID := update.Message.From.ID
	if userID == h.cfg.AdminID {
		var fileId string
		switch {
		case len(update.Message.Photo) > 0:
			fileId = update.Message.Photo[len(update.Message.Photo)-1].FileID
		case update.Message.Video != nil:
			fileId = update.Message.Video.FileID
		}
		if fileId != "" {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: h.cfg.AdminID,
				Text:   fileId,
			})
			if err != nil {
				h.logger.Error("error send fileId to admin", zap.Error(err))
			}
		}
	}

	userState := h.getOrCreateUserState(ctx, userID)

	// Инлайн-кнопка с эмодзи и ссылкой
	markup := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{
				Text: "📲 Открыть MediHub",
				URL:  "https://t.me/dariger_test_bot/mediHub",
			},
		}},
	}

	switch userState.State {
	case stateAdminPanel:
		h.AdminHandler(ctx, b, update)
		return
	case stateBroadcast:
		h.SendMessage(ctx, b, update)
		return
	case stateScreenChat:
		h.handleScreenChat(ctx, b, update)
		return
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

// SubmitScreeningHandler handles POST requests to submit screening results
func (h *Handler) SubmitScreeningHandler(w http.ResponseWriter, r *http.Request, ctx context.Context, b *bot.Bot) {
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
	var result ScreeningResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		h.logger.Error("Failed to decode screening request", zap.Error(err))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if result.UserID == 0 {
		http.Error(w, "Missing user ID", http.StatusBadRequest)
		return
	}

	// Send immediate response to frontend
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Screening data received successfully",
		"user_id": result.UserID,
	})

	// Process in background
	go func() {
		h.logger.Info("Processing screening data",
			zap.Int64("user_id", result.UserID),
			zap.String("language", result.Language))

		// Send the pre-formatted message to admin
		h.sendFormattedScreeningToAdmin(ctx, b, &result)
	}()
}

// Send the pre-formatted screening message to admin (no parsing needed!)
func (h *Handler) sendFormattedScreeningToAdmin(ctx context.Context, b *bot.Bot, result *ScreeningResult) {
	// Use the pre-formatted message from frontend
	msgText := result.FormattedMessage

	// If no formatted message provided, create a basic one
	if msgText == "" {
		h.logger.Warn("No formatted message provided, creating basic message",
			zap.Int64("user_id", result.UserID))

		// Create basic fallback message
		timestamp := result.Timestamp
		if parsedTime, err := time.Parse(time.RFC3339, result.Timestamp); err == nil {
			timestamp = parsedTime.Format("2006-01-02 15:04:05")
		}

		langText := map[string]string{
			"kz": "🇰🇿 Қазақша",
			"ru": "🇷🇺 Русский",
		}[result.Language]
		if langText == "" {
			langText = result.Language
		}

		msgText = fmt.Sprintf(
			"📋 ЖАҢА СКРИНИНГ ДЕРЕКТЕРІ / НОВЫЕ ДАННЫЕ СКРИНИНГА\n\n"+
				"👤 Пайдаланушы ID / ID пользователя: %d\n"+
				"🕒 Уақыт / Время: %s\n"+
				"🌐 Тіл / Язык: %s\n\n"+
				"⚠️ Форматталған хабарлама жоқ / Нет форматированного сообщения\n\n"+
				"👨‍⚕️ ДӘРІГЕРДІҢ ҚОРЫТЫНДЫСЫ КҮТІЛУДЕ / ОЖИДАЕТСЯ ЗАКЛЮЧЕНИЕ ВРАЧА",
			result.UserID,
			timestamp,
			langText,
		)
	}

	// Split message if too long (Telegram limit is 4096 characters)
	const maxLength = 4000
	messages := h.splitLongMessage(msgText, maxLength)

	// Create inline keyboard with contact button (only for last message)
	markup := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{{
			{
				Text:         "💬 Пайдаланушымен сөйлесу / Связаться с пользователем",
				CallbackData: fmt.Sprintf("screen_%d", result.UserID),
			},
		}},
	}

	// Send all message parts
	for i, msg := range messages {
		var sendMarkup *models.InlineKeyboardMarkup
		if i == len(messages)-1 { // Only add buttons to last message
			sendMarkup = markup
		}

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      h.cfg.AdminID,
			Text:        msg,
			ReplyMarkup: sendMarkup,
		})

		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      h.cfg.AdminID2,
			Text:        msg,
			ReplyMarkup: sendMarkup,
		})

		if err != nil {
			h.logger.Error("Failed to send screening message to admin",
				zap.Error(err),
				zap.Int64("user_id", result.UserID),
				zap.Int("message_part", i+1))
		} else if i == len(messages)-1 {
			h.logger.Info("Screening data sent to admin successfully",
				zap.Int64("user_id", result.UserID),
				zap.Int("total_parts", len(messages)))
		}

		// Small delay between messages to avoid rate limiting
		if i < len(messages)-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// Helper function to split long messages while preserving formatting
func (h *Handler) splitLongMessage(text string, maxLength int) []string {
	if len(text) <= maxLength {
		return []string{text}
	}

	var messages []string
	lines := strings.Split(text, "\n")
	currentMsg := ""

	for _, line := range lines {
		// Check if adding this line would exceed the limit
		testMsg := currentMsg
		if currentMsg != "" {
			testMsg += "\n"
		}
		testMsg += line

		if len(testMsg) <= maxLength {
			currentMsg = testMsg
		} else {
			// Current message is full, start a new one
			if currentMsg != "" {
				messages = append(messages, strings.TrimSpace(currentMsg))
			}

			// If single line is too long, truncate it
			if len(line) > maxLength {
				line = line[:maxLength-3] + "..."
			}
			currentMsg = line
		}
	}

	// Add the last message if there's content
	if currentMsg != "" {
		messages = append(messages, strings.TrimSpace(currentMsg))
	}

	return messages
}

// Handler for the screen callback (when admin clicks contact user button)
func (h *Handler) HandleScreenCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	callback := update.CallbackQuery
	if callback == nil {
		return
	}

	// Extract user ID from callback data (format: "screen_123456")
	parts := strings.Split(callback.Data, "_")
	if len(parts) != 2 || parts[0] != "screen" {
		return
	}

	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		h.logger.Error("Invalid user ID in callback", zap.String("callback_data", callback.Data))
		return
	}

	// Answer the callback query
	_, err = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
		Text:            "Пайдаланушымен байланысу / Связаться с пользователем",
	})
	if err != nil {
		h.logger.Error("Failed to answer callback query", zap.Error(err))
	}

	// Create message to contact the user
	contactMsg := fmt.Sprintf(
		"👨‍⚕️ ДӘРІГЕРДЕН ХАБАРЛАМА / СООБЩЕНИЕ ОТ ВРАЧА\n\n" +
			"Сәлеметсіз бе! Сіздің скрининг тестіңіздің нәтижелері бойынша дәрігер сізбен хабарласқысы келеді.\n\n" +
			"Здравствуйте! По результатам вашего скрининг теста врач хочет связаться с вами.\n\n" +
			"📞 Байланыс үшін: / Для связи:\n" +
			"• Телефон: +7 (XXX) XXX-XX-XX\n" +
			"• Мекен-жай: / Адрес: [CLINIC_ADDRESS]\n\n" +
			"⏰ Жұмыс уақыты / Время работы: 08:00-20:00",
	)

	// Send message to user
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: userID,
		Text:   contactMsg,
	})

	if err != nil {
		h.logger.Error("Failed to send contact message to user",
			zap.Error(err),
			zap.Int64("user_id", userID))

		// Notify admin about the error
		errorMsg := fmt.Sprintf(
			"❌ Пайдаланушыға хабарлама жіберу мүмкін болмады / Не удалось отправить сообщение пользователю\n"+
				"👤 User ID: %d\n"+
				"🔗 Қолмен хабарласыңыз / Свяжитесь вручную",
			userID,
		)

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   errorMsg,
		})
	} else {
		h.logger.Info("Contact message sent to user",
			zap.Int64("user_id", userID))

		// Confirm to admin
		confirmMsg := fmt.Sprintf(
			"✅ Пайдаланушыға хабарлама жіберілді / Сообщение отправлено пользователю\n"+
				"👤 User ID: %d",
			userID,
		)

		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: h.cfg.AdminID,
			Text:   confirmMsg,
		})
	}
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
		for _, admin := range []int64{h.cfg.AdminID} {
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
				h.logger.Warn("Ошибка открытия файла", zap.String("path", photoPath), zap.Error(err))
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

	// убираем spinner
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Қабылдадым!",
	})

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
		photoPath := ""
		if doc.AvatarPath != nil {
			photoPath = *doc.AvatarPath
		}
		caption := fmt.Sprintf("Ваш врач: %s\nКонтакт: %s", *doc.FullName, *doc.Contact)
		if photoPath != "" {
			f, err = os.Open(photoPath)
			if err != nil {
				h.logger.Error("error in open doctor ava", zap.Error(err))
			}
		}
		if f != nil {
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
		} else {
			// Send message without photo if file opening failed
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: userID,
				Text:   caption,
			})
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

	// Screening submit endpoint
	http.HandleFunc("/api/screening/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.SubmitScreeningHandler(w, r, ctx, b)
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

	// Screening route - serves the screening page inside Mini App
	http.HandleFunc("/screening", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/screening.html"

		// Check if the screening template exists
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Screening template not found", zap.String("path", templatePath))
			http.Error(w, "Screening page not found", http.StatusNotFound)
			return
		}

		// Set proper headers for HTML content
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// Serve the screening HTML file
		http.ServeFile(w, r, templatePath)
	})

	// Alternative static file serving if you prefer to keep it in static folder
	http.HandleFunc("/static/screening.html", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./static/screening.html"

		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			h.logger.Error("Screening template not found", zap.String("path", templatePath))
			http.Error(w, "Screening page not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, templatePath)
	})
	http.HandleFunc("/welcome", func(w http.ResponseWriter, r *http.Request) {
		templatePath := "./server/templates/welcome.html"
		if _, err := os.Stat(templatePath); os.IsNotExist(err) {
			http.Redirect(w, r, "/welcome", http.StatusFound)
			return
		}
		http.ServeFile(w, r, templatePath)
	})
	/*
		// Welcome page route with user ID
			http.HandleFunc("/welcome/", func(w http.ResponseWriter, r *http.Request) {
				// Extract user ID from URL path
				pathParts := strings.Split(r.URL.Path, "/")
				if len(pathParts) < 3 || pathParts[2] == "" {
					// If no user ID provided, redirect to main welcome page
					http.Redirect(w, r, "/welcome", http.StatusFound)
					return
				}

				userIDStr := pathParts[2]
				userTelegramID, err := strconv.ParseInt(userIDStr, 10, 64)
				if err != nil {
					h.logger.Error("Invalid user ID format", zap.String("userID", userIDStr), zap.Error(err))
					http.Redirect(w, r, "/welcome", http.StatusFound)
					return
				}

				// Get user status
				status, err := h.userRepo.GetUserStatus(userTelegramID, h.repo)
				if err != nil {
					h.logger.Error("Error getting user status for welcome page", zap.Error(err))
					http.Redirect(w, r, "/welcome", http.StatusFound)
					return
				}
				fmt.Println(userIDStr)
				fmt.Println(status)

				// Serve appropriate welcome page based on user type
				var templatePath string
				if status.IsDoctor {
					templatePath = "./static/update-doctor.html"
				} else {
					templatePath = "./static/welcome.html"
				}

				if _, err := os.Stat(templatePath); os.IsNotExist(err) {
					h.logger.Error("Welcome template not found", zap.String("path", templatePath))
					// Fallback to main welcome page
					http.Redirect(w, r, "/welcome", http.StatusFound)
					return
				}
				http.ServeFile(w, r, templatePath)
			})
	*/

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "OK",
			"service": "MedHub API",
			"time":    time.Now().Format(time.RFC3339),
		})
	})

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	h.logger.Info("Starting web server", zap.String("port", port))
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		h.logger.Fatal("Failed to start web server", zap.Error(err))
	}
}
