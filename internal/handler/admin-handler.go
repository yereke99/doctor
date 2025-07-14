package handler

import (
	"context"
	"doctor/internal/domain"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	stateStart      string = "start"
	stateCount      string = "count"
	statePaid       string = "paid"
	stateContact    string = "contact"
	stateAdminPanel string = "admin_panel"
	stateBroadcast  string = "broadcast"
	stateScreenChat        = "screen_chat"
)

func (h *Handler) AdminHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From.ID != h.cfg.AdminID || update.Message.From.ID != h.cfg.AdminID2 {
		return
	}

	adminId := update.Message.From.ID
	h.logger.Info("Admin handler", zap.Any("update", update))

	state, err := h.redisRepo.GetUserState(ctx, adminId)
	if err != nil {
		h.logger.Error("Failed to get admin state from Redis", zap.Error(err))
	}
	if state != nil && state.State == stateBroadcast {
		h.SendMessage(ctx, b, update)
		return
	}

	adminKeyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: "👥 Тіркелгендер (Just Clicked)"},
				{Text: "🛍 Клиенттер (Clients)"},
			},
			{
				{Text: "📢 Хабарлама (Messages)"},
				{Text: "❌ Жабу (Close)"},
			},
		},
		ResizeKeyboard:  true,
		Selective:       true,
		OneTimeKeyboard: true,
	}

	switch update.Message.Text {
	case "/admin":
		newAdminState := &domain.UserState{
			State: stateAdminPanel,
		}
		if err := h.redisRepo.SaveUserState(ctx, adminId, newAdminState); err != nil {
			h.logger.Error("Failed to save admin state to Redis", zap.Error(err))
		}
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      adminId,
			Text:        "🔧 Админ панеліне қош келдіңіз!\n\nТаңдаңыз:",
			ReplyMarkup: adminKeyboard,
		})
		if err != nil {
			h.logger.Error("Failed to send admin panel", zap.Error(err))
		}

	case "👥 Тіркелгендер (Just Clicked)":
		h.handleJustUsers(ctx, b)

	case "🛍 Клиенттер (Clients)":
		h.handleClients(ctx, b)

	case "📢 Хабарлама (Messages)":
		h.handleBroadcastMenu(ctx, b)

	case "📊 Статистика (Statistics)":
		h.handleStatistics(ctx, b)

	case "❌ Жабу (Close)":
		h.handleCloseAdmin(ctx, b)
	default:
		if state != nil && state.State == stateAdminPanel {
			_, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      adminId,
				Text:        "Белгісіз команда. Төмендегі батырмаларды пайдаланыңыз:",
				ReplyMarkup: adminKeyboard,
			})
			if err != nil {
				h.logger.Error("Failed to send admin panel", zap.Error(err))
			}
		}
	}
}

func (h *Handler) handleStatistics(ctx context.Context, b *bot.Bot) {
	userIds, _ := h.userRepo.GetAllJustUserIDs(ctx)

	message := fmt.Sprintf(`📊 ЖАЛПЫ СТАТИСТИКА

👥 Жалпы пайдаланушылар: %d
🛍 Клиенттер: 0
🎲 Лото қатысушылары: 0

📅 Соңғы жаңарту: %s`,
		len(userIds),
		time.Now().Format("2006-01-02 15:04:05"))

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send statistics", zap.Error(err))
	}
}

func (h *Handler) handleCloseAdmin(ctx context.Context, b *bot.Bot) {
	if err := h.redisRepo.DeleteUserState(ctx, h.cfg.AdminID); err != nil {
		h.logger.Error("Failed to delete admin state from Redis", zap.Error(err))
	}

	// Remove keyboard
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   "✅ Админ панелі жабылды",
		ReplyMarkup: &models.ReplyKeyboardRemove{
			RemoveKeyboard: true,
		},
	})
	if err != nil {
		h.logger.Error("Failed to close admin panel", zap.Error(err))
	}
}

func (h *Handler) SendMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From.ID != h.cfg.AdminID {
		return
	}
	adminId := h.cfg.AdminID
	// here

	msgType, fileId, caption := h.parseMessage(update.Message)
	var userIds []int64

	if len(userIds) == 0 {
		_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: adminId,
			Text:   "📭 Хабарлама жіберуге пайдаланушылар табылмады",
		})
		if sendErr != nil {
			h.logger.Error("Failed to send no users message", zap.Error(sendErr))
		}
		return
	}

	statusMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminId,
		Text:   fmt.Sprintf("📤 Хабарлама жіберіліп жатыр...\n👥 Жалпы: %d пайдаланушы", len(userIds)),
	})
	if err != nil {
		h.logger.Error("Failed to send status message", zap.Error(err))
		return
	}

	limiter := rate.NewLimiter(rate.Every(time.Second/30), 1)

	var wg sync.WaitGroup
	var successCount, failedCount int64
	for i := 0; i < len(userIds); i++ {
		if err := limiter.Wait(ctx); err != nil {
			h.logger.Error("Rate limiter wait error", zap.Error(err))
			break
		}
		wg.Add(1)
		go func(userId int64) {
			defer wg.Done()
			if err := h.sendToUserBroadcast(ctx, b, update, userId, msgType, fileId, caption); err != nil {
				atomic.AddInt64(&failedCount, 1)
				h.logger.Warn("Failed to send message to user", zap.Int64("user", userId), zap.Error(err))
			} else {
				atomic.AndInt64(&successCount, 1)
			}
		}(userIds[i])
	}
	wg.Wait()
	// Send final results
	finalSuccess := atomic.LoadInt64(&successCount)
	finalFailed := atomic.LoadInt64(&failedCount)
	successRate := float64(finalSuccess) / float64(len(userIds)) * 100

	finalText := fmt.Sprintf(`✅ ХАБАРЛАМА ЖІБЕРУ АЯҚТАЛДЫ!

👥 Жалпы: %d пайдаланушы
✅ Сәтті: %d
❌ Қате: %d
📊 Сәттілік: %.1f%%

📋 Хабарлама түрі: %s
⏰ Уақыт: %s`,
		len(userIds),
		finalSuccess,
		finalFailed,
		successRate,
		//h.getBroadcastTypeName(broadcastType),
		time.Now().Format("2006-01-02 15:04:05"))

	if statusMsg != nil {
		b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    adminId,
			MessageID: statusMsg.ID,
			Text:      finalText,
		})
	}

	// Log broadcast results
	h.logger.Info("Broadcast completed",
		//zap.String("type", broadcastType),
		zap.Int("total", len(userIds)),
		zap.Int64("success", finalSuccess),
		zap.Int64("failed", finalFailed),
		zap.Float64("success_rate", successRate))

	/*
		if err := h.redisRepo.DeleteUserState(ctx, adminId); err != nil {
			h.logger.Error("Failed to delete admin state from Redis", zap.Error(err))
		}
	*/
	time.Sleep(2 * time.Second)
	h.AdminHandler(ctx, b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: adminId},
			Text: "/admin",
		},
	})
}

func (h *Handler) handleJustUsers(ctx context.Context, b *bot.Bot) {
	userIds, err := h.userRepo.GetAllJustUserIDs(ctx)
	if err != nil {
		h.logger.Error("Failed to get just users", zap.Error(err))
		return
	}

	message := fmt.Sprintf("👥 ТІРКЕЛГЕН ПАЙДАЛАНУШЫЛАР\n\nЖалпы: %d пайдаланушы", len(userIds))
	_, err = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   message,
	})
	if err != nil {
		h.logger.Error("Failed to send just users", zap.Error(err))
	}
}

func (h *Handler) handleClients(ctx context.Context, b *bot.Bot) {
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: h.cfg.AdminID,
		Text:   "🛍 КЛИЕНТТЕР\n\n🔧 Дамуда...",
	})
	if err != nil {
		h.logger.Error("Failed to send clients", zap.Error(err))
	}
}

func (h *Handler) sendToUserBroadcast(ctx context.Context, b *bot.Bot, update *models.Update, chatId int64, msgType, fileID, caption string) error {
	switch msgType {
	case "text":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatId,
			Text:   caption,
		})
		return err
	case "photo":
		_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatId,
			Photo:   &models.InputFileString{Data: fileID},
			Caption: caption,
		})
		return err
	case "video":
		_, err := b.SendVideo(ctx, &bot.SendVideoParams{ChatID: chatId, Video: &models.InputFileString{Data: fileID}, Caption: caption, ProtectContent: true})
		return err
	case "document":
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatId, Document: &models.InputFileString{Data: fileID}, Caption: caption, ProtectContent: true})
		return err
	case "video_note":
		_, err := b.SendVideoNote(ctx, &bot.SendVideoNoteParams{ChatID: chatId, VideoNote: &models.InputFileString{Data: fileID}, ProtectContent: true})
		return err
	case "audio":
		_, err := b.SendAudio(ctx, &bot.SendAudioParams{ChatID: chatId, Audio: &models.InputFileString{Data: fileID}, ProtectContent: true})
		return err
	default:
		return nil
	}
}

func (h *Handler) parseMessages(msg *models.Message) (msgType, fileId, caption string) {
	switch {
	case msg.Text != "":
		return "text", "", msg.Text
	case len(msg.Photo) > 0:
		return "photo", msg.Photo[len(msg.Photo)-1].FileID, msg.Caption
	case msg.Video != nil:
		return "video", msg.Video.FileID, msg.Caption
	case msg.Document != nil:
		return "document", msg.Document.FileID, msg.Caption
	case msg.VideoNote != nil:
		return "video_note", msg.VideoNote.FileID, msg.Caption
	case msg.Audio != nil:
		return "audio", msg.Audio.FileID, msg.Caption
	case msg.Location != nil:
		locationStr := fmt.Sprintf("%.6f,%.6f", msg.Location.Latitude, msg.Location.Longitude)
		return "location", "", locationStr
	case msg.Contact != nil:
		contactStr := fmt.Sprintf("%s: %s", msg.Contact.FirstName, msg.Contact.PhoneNumber)
		return "contact", "", contactStr
	default:
		return "", "", ""
	}
}

func (h *Handler) getBroadcastTypeName(broadcastType string) string {
	switch broadcastType {
	case "all":
		return "Барлық пайдаланушылар"
	case "clients":
		return "Барлық клиенттер"
	case "loto":
		return "Лото қатысушылары"
	case "just":
		return "Тіркелген пайдаланушылар"
	default:
		return "Белгісіз"
	}
}

// Helper methods for admin panel
func (h *Handler) handleBroadcastMenu(ctx context.Context, b *bot.Bot) {
	adminId := h.cfg.AdminID

	// Get counts for each category
	allCount, _ := h.userRepo.GetAllJustUserIDs(ctx)

	broadcastState := &domain.UserState{
		State: stateBroadcast,
	}
	if err := h.redisRepo.SaveUserState(ctx, adminId, broadcastState); err != nil {
		h.logger.Error("Failed to save broadcast state to Redis", zap.Error(err))
	}

	broadcastKeyboard := &models.ReplyKeyboardMarkup{
		Keyboard: [][]models.KeyboardButton{
			{
				{Text: "📢 Барлығына жіберу"},
				{Text: "🛍 Клиенттерге жіберу"},
			},
			{
				{Text: "🎲 Лото қатысушыларына "},
				{Text: "👥 Тіркелгендерге"},
			},
			{
				{Text: "🔙 Артқа (Back)"},
			},
		},
		ResizeKeyboard:  true,
		OneTimeKeyboard: false,
	}

	message := fmt.Sprintf(`📢 ХАБАРЛАМА ЖІБЕРУ

📊 Қол жетімді аудитория:
• 👥 Барлық пайдаланушылар: %d
• 🛍 Клиенттер: %d  
• 🎲 Лото қатысушылары: %d
• 📅 Тіркелгендер: %d

⚠️ Ескерту: Хабарлама барлық таңдалған пайдаланушыларға жіберіледі. Сақ болыңыз!

Қайсы топқа хабарлама жіберуді қалайсыз?`,
		len(allCount), len(allCount), len(allCount), len(allCount))

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      adminId,
		Text:        message,
		ReplyMarkup: broadcastKeyboard,
	})
	if err != nil {
		h.logger.Error("Failed to send broadcast menu", zap.Error(err))
	}
}

func (h *Handler) startBroadcast(ctx context.Context, b *bot.Bot, broadcastType string) {
	adminId := h.cfg.AdminID

	// Set admin to broadcast state
	broadCastState := &domain.UserState{
		State:         stateBroadcast,
		BroadCastType: broadcastType,
	}
	if err := h.redisRepo.SaveUserState(ctx, adminId, broadCastState); err != nil {
		h.logger.Error("Failed to save broadcast state to Redis", zap.Error(err))
	}

	targetDescription := h.getBroadcastTypeName(broadcastType)

	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: adminId,
		Text: fmt.Sprintf(`📝 ХАБАРЛАМА ЖАЗУ

🎯 Мақсатты аудитория: %s

💡 Қолдаулатын форматтар:
• 📝 Мәтін хабарлама
• 📷 Фото + мәтін
• 🎥 Видео + мәтін  
• 📎 Файл + мәтін
• 🎵 Аудио
• 🎬 GIF анимация

Хабарламаңызды жіберіңіз:`, targetDescription),
		ReplyMarkup: &models.ReplyKeyboardMarkup{
			Keyboard: [][]models.KeyboardButton{
				{{Text: "🔙 Артқа (Back)"}},
			},
			ResizeKeyboard:  true,
			OneTimeKeyboard: false,
		},
	})
	if err != nil {
		h.logger.Error("Failed to start broadcast", zap.Error(err))
	}
}
