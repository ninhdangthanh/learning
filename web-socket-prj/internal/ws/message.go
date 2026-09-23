package ws

import (
	"encoding/json"
	"errors"
)

const (
	ErrorCodeMalformedMessage = "MALFORMED_MESSAGE"
	ErrorCodeInternal         = "INTERNAL_ERROR"
)

var errMissingMessageField = errors.New(`field "message" is required`)

type EchoMessage struct {
	Message string `json:"message"`
}

type ErrorMessage struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newErrorMessage(code, message string) ErrorMessage {
	return ErrorMessage{Type: "error", Code: code, Message: message}
}

func decodeEchoMessage(payload []byte) (EchoMessage, error) {
	var raw struct {
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return EchoMessage{}, err
	}
	if raw.Message == nil {
		return EchoMessage{}, errMissingMessageField
	}
	return EchoMessage{Message: *raw.Message}, nil
}
