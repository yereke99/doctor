package handler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

func (h *Handler) AdminHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.From.ID != h.cfg.AdminID {
		return
	}

	h.logger.Info("admin clicked")
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
