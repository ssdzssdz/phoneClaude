package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type FCMSender struct {
	serverKey string
	client    *http.Client
}

type FCMMessage struct {
	To           string                 `json:"to"`
	Data         map[string]interface{} `json:"data"`
	Notification *FCMNotification       `json:"notification,omitempty"`
}

type FCMNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type FCMResponse struct {
	Success int `json:"success"`
	Failure int `json:"failure"`
}

func NewFCMSender(serverKey string) *FCMSender {
	return &FCMSender{
		serverKey: serverKey,
		client:    &http.Client{},
	}
}

func (f *FCMSender) Send(msg FCMMessage) error {
	body, _ := json.Marshal(msg)
	req, err := http.NewRequest("POST",
		"https://fcm.googleapis.com/fcm/send",
		bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "key="+f.serverKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fcm send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var fcmResp FCMResponse
	json.Unmarshal(respBody, &fcmResp)

	if fcmResp.Failure > 0 {
		return fmt.Errorf("fcm partial failure: %s", string(respBody))
	}
	return nil
}

func (f *FCMSender) NotifySessionDone(sessionID, sessionName, token string) error {
	return f.Send(FCMMessage{
		To: token,
		Data: map[string]interface{}{
			"session_id": sessionID,
		},
		Notification: &FCMNotification{
			Title: "Claude Code",
			Body:  fmt.Sprintf("Session '%s' has completed", sessionName),
		},
	})
}
