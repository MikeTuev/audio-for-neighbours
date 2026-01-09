package main

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type telegramNotifier struct {
	bot    *tgbotapi.BotAPI
	chatID int64
}

func newTelegramNotifier(token string, chatID int64) (*telegramNotifier, error) {
	if token == "" || chatID == 0 {
		return nil, nil
	}
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	return &telegramNotifier{bot: bot, chatID: chatID}, nil
}

func (t *telegramNotifier) run(ctx context.Context, handler func(string) string) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := t.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			if update.Message.Chat == nil || update.Message.Chat.ID != t.chatID {
				continue
			}
			cmd := update.Message.Command()
			if cmd == "" {
				continue
			}
			resp := handler(cmd)
			t.send(resp)
		}
	}
}

func (t *telegramNotifier) send(msg string) {
	if t == nil {
		return
	}
	_, _ = t.bot.Send(tgbotapi.NewMessage(t.chatID, msg))
}

func (t *telegramNotifier) sendPhotoBytes(filename string, data []byte) {
	if t == nil {
		return
	}
	photo := tgbotapi.NewPhoto(t.chatID, tgbotapi.FileBytes{
		Name:  filename,
		Bytes: data,
	})
	_, _ = t.bot.Send(photo)
}
