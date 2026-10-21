// Package telegram is the Telegram auth provider (Mini App initData) plus Telegram helpers: Login
// Widget validation, Mini App links and the Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// SendMessage sends a plain-text message from the bot to a Telegram chat (a user's chat ID is
// their Telegram ID). Telegram refuses (403) until the user has started the bot or allowed it to
// write (the Mini App asks with requestWriteAccess).
func SendMessage(ctx context.Context, botToken string, chatID int64, text string) error {
	return botCall(ctx, botToken, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}

// PrepareShare stores a message the user can send to any chat from the Mini App (Bot API 8.0
// savePreparedInlineMessage; the client opens Telegram's picker with shareMessage(id)). The message
// carries one button opening the Mini App at link, if set. Returns the prepared message ID.
func PrepareShare(ctx context.Context, botToken string, telegramID int64, id, title, text, button, link string) (string, error) {
	result := map[string]any{
		"type": "article", "id": id, "title": title,
		"input_message_content": map[string]any{"message_text": text},
	}
	if link != "" {
		result["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]any{{{"text": button, "url": link}}}}
	}
	return PrepareMessage(ctx, botToken, telegramID, result)
}

// PrepareMessage saves any inline result (a photo, say) for the user to send with shareMessage.
func PrepareMessage(ctx context.Context, botToken string, telegramID int64, result map[string]any) (string, error) {
	var r struct {
		ID string `json:"id"`
	}
	err := botCall(ctx, botToken, "savePreparedInlineMessage", map[string]any{
		"user_id": telegramID, "result": result,
		"allow_user_chats": true, "allow_bot_chats": true, "allow_group_chats": true, "allow_channel_chats": true,
	}, &r)
	return r.ID, err
}

// CallBot calls any Bot API method (the admin bot: getUpdates, inline keyboards). Errors never
// contain the token.
func CallBot(ctx context.Context, botToken, method string, params, out any) error {
	return botCall(ctx, botToken, method, params, out)
}

// botCall posts a Bot API method and decodes its result into out (if set).
func botCall(ctx context.Context, botToken, method string, params, out any) error {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+botToken+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The URL holds the bot token and *url.Error prints it: keep only the cause.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	var r struct {
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram %s: %d %s", method, resp.StatusCode, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}
