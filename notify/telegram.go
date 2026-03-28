package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"openclaw-guard-kit/internal/protocol"
)

// TelegramNotifier sends messages via Telegram Bot API.
// It reads the token and chat ID from the unified store.
type TelegramNotifier struct{}

// Notify sends a formatted event message to the bound chat.
// It gets the token from credentials store and chat ID from bindings store.
func (t TelegramNotifier) Notify(ctx context.Context, e protocol.Event) error {
	if isQuietEventType(e.Type) {
		return nil
	}

	text := buildChannelEventText(e)
	if strings.TrimSpace(text) == "" {
		return nil
	}

	// Get token from credentials store
	token := GetTelegramToken()

	// Get chat ID from bindings store
	store := getStore()
	var chatID int64

	if store != nil {
		bindings := store.ListBindings()
		for _, binding := range bindings {
			if binding.Channel == "telegram" && binding.Status == BindingStatusBound {
				parsedID, err := strconv.ParseInt(binding.SenderID, 10, 64)
				if err != nil {
					log.Printf("telegram notifier: invalid chatID in binding: %v", err)
					continue
				}
				chatID = parsedID
				break
			}
		}
	}

	// Fallback to environment variables if not found in stores
	if token == "" {
		token = os.Getenv("TELEGRAM_BOT_TOKEN")
	}
	if chatID == 0 && token != "" {
		chatIDStr := os.Getenv("TELEGRAM_CHAT_ID")
		if chatIDStr != "" {
			parsedID, err := strconv.ParseInt(chatIDStr, 10, 64)
			if err == nil {
				chatID = parsedID
			}
		}
	}

	if token == "" || chatID == 0 {
		log.Printf("telegram notifier not configured; token or chatID missing")
		return nil
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("telegram response: %s", resp.Status)
}

// StartTelegramBindingFlow starts a long polling loop to bind the bot to a chat.
// It verifies the token, then waits for the user to send a message to the bot.
// When a message is received, it uses the chat ID from that message to create a binding.
// It returns the chat ID when binding succeeds, or an error if setup fails.
// The binding is NOT saved here; the caller must save the binding using the store.
// The caller must cancel the context to stop polling.
// Note: This function remains unchanged to preserve the existing behavior while we migrate storage.
// The actual binding storage will be handled by the caller using the store.
func StartTelegramBindingFlow(token string) (int64, error) {
	if token == "" {
		return 0, fmt.Errorf("telegram token not provided")
	}
	// Verify token validity by calling getMe
	u := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)
	resp, err := http.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("invalid token: %s", resp.Status)
	}
	var respData struct {
		Ok     bool `json:"ok"`
		Result struct {
			ID int64 `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return 0, err
	}
	if !respData.Ok {
		return 0, fmt.Errorf("invalid token response")
	}
	// Start long polling
	var offset int64 = 0
	for {
		// We'll use a fixed timeout of 30 seconds for the long polling.
		v := url.Values{}
		v.Set("offset", strconv.FormatInt(offset+1, 10))
		v.Set("timeout", "30") // long polling
		u := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", token, v.Encode())
		resp, err := http.Get(u)
		if err != nil {
			log.Printf("telegram getUpdates error: %v, retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}
		var updates struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID int64 `json:"update_id"`
				Message  struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
					Text string `json:"text"`
				} `json:"message,omitempty"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&updates); err != nil {
			resp.Body.Close()
			log.Printf("telegram getUpdates decode error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()
		if !updates.Ok {
			continue
		}
		for _, upd := range updates.Result {
			offset = upd.UpdateID // update offset to latest
			if upd.Message.Chat.ID == 0 {
				continue
			}
			chatID := upd.Message.Chat.ID
			return chatID, nil
		}
	}
}

// SendTelegramMessage sends a message to a specific chat ID using the provided token.
// This is a shared implementation for both automatic confirmation and manual test messages.
// Returns (success, errorMessage) tuple.
func SendTelegramMessage(token string, chatID int64, text string) (bool, string) {
	if token == "" {
		return false, "token is empty"
	}
	if chatID == 0 {
		return false, "chatID is empty"
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Sprintf("failed to marshal payload: %v", err)
	}

	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Sprintf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	// Parse response to check if OK
	var result struct {
		Ok bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Sprintf("failed to parse response: %v", err)
	}

	if !result.Ok {
		return false, "Telegram API returned error"
	}

	return true, ""
}

// Register the notifier
func init() {
	RegisterNotifier(TelegramNotifier{})
}
