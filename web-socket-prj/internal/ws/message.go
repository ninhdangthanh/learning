package ws

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	ErrorCodeMalformedMessage       = "MALFORMED_MESSAGE"
	ErrorCodeUnsupportedMessageType = "UNSUPPORTED_MESSAGE_TYPE"
	ErrorCodeInternal               = "INTERNAL_ERROR"
)

const (
	messageTypeChat  = "message"
	messageTypeError = "error"
)

var errMissingMessageField = errors.New(`field "message" is required`)

type InboundMessage struct {
	Message string `json:"message"`
}

type ChatMessage struct {
	Type     string    `json:"type"`
	SenderID string    `json:"sender_id"`
	Message  string    `json:"message"`
	SentAt   time.Time `json:"sent_at"`
}

type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newChatMessage(senderID, message string, sentAt time.Time) ChatMessage {
	return ChatMessage{Type: messageTypeChat, SenderID: senderID, Message: message, SentAt: sentAt}
}

func newErrorMessage(code, message string) ErrorMessage {
	return ErrorMessage{Type: messageTypeError, Code: code, Message: message}
}

func decodeInboundMessage(payload []byte) (InboundMessage, error) {
	var raw struct {
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return InboundMessage{}, err
	}
	if raw.Message == nil {
		return InboundMessage{}, errMissingMessageField
	}
	return InboundMessage{Message: *raw.Message}, nil
}
