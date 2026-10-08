package ws

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	ErrorCodeMalformedMessage       = "MALFORMED_MESSAGE"
	ErrorCodeUnsupportedMessageType = "UNSUPPORTED_MESSAGE_TYPE"
	ErrorCodeUnknownType            = "UNKNOWN_TYPE"
	ErrorCodeInvalidRoomID          = "INVALID_ROOM_ID"
	ErrorCodeNotInRoom              = "NOT_IN_ROOM"
	ErrorCodeTooManyRooms           = "TOO_MANY_ROOMS"
	ErrorCodeContentTooLong         = "CONTENT_TOO_LONG"
	ErrorCodeInternal               = "INTERNAL_ERROR"
)

const (
	messageTypeJoinRoom  = "join_room"
	messageTypeLeaveRoom = "leave_room"
	messageTypeChat      = "message"
	messageTypeJoined    = "joined"
	messageTypeLeft      = "left"
	messageTypeError     = "error"
)

const (
	MaxRoomsPerClient = 50
	MaxContentLength  = 2000
)

var roomIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type inboundEnvelope struct {
	Type    string `json:"type"`
	RoomID  string `json:"room_id"`
	Content string `json:"content"`
}

type outboundMessage struct {
	Type     string    `json:"type"`
	RoomID   string    `json:"room_id"`
	SenderID string    `json:"sender_id"`
	Content  string    `json:"content"`
	SentAt   time.Time `json:"sent_at"`
}

type outboundAck struct {
	Type   string `json:"type"`
	RoomID string `json:"room_id"`
}

type ErrorMessage struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	RequestType string `json:"request_type,omitempty"`
	RoomID      string `json:"room_id,omitempty"`
}

func newOutboundMessage(roomID, senderID, content string, sentAt time.Time) outboundMessage {
	return outboundMessage{Type: messageTypeChat, RoomID: roomID, SenderID: senderID, Content: content, SentAt: sentAt}
}

func newAck(ackType, roomID string) outboundAck {
	return outboundAck{Type: ackType, RoomID: roomID}
}

func newErrorMessage(code, message string) ErrorMessage {
	return ErrorMessage{Type: messageTypeError, Code: code, Message: message}
}

func newRequestError(code, message string, request inboundEnvelope) *ErrorMessage {
	errorMessage := newErrorMessage(code, message)
	errorMessage.RequestType = request.Type
	errorMessage.RoomID = request.RoomID
	return &errorMessage
}

func encodeTextFrame(value any) (outboundFrame, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return outboundFrame{}, err
	}
	return outboundFrame{messageType: websocket.TextMessage, payload: payload}, nil
}

func decodeInbound(payload []byte) (inboundEnvelope, *ErrorMessage) {
	var raw struct {
		Type    *string `json:"type"`
		RoomID  *string `json:"room_id"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return inboundEnvelope{}, newRequestError(ErrorCodeMalformedMessage, err.Error(), inboundEnvelope{})
	}
	if raw.Type == nil {
		return inboundEnvelope{}, newRequestError(ErrorCodeMalformedMessage, `field "type" is required`, inboundEnvelope{})
	}

	envelope := inboundEnvelope{Type: *raw.Type, RoomID: valueOrEmpty(raw.RoomID), Content: valueOrEmpty(raw.Content)}
	switch envelope.Type {
	case messageTypeJoinRoom, messageTypeLeaveRoom:
		return envelope, validateRoomID(envelope, raw.RoomID != nil)
	case messageTypeChat:
		if protoErr := validateRoomID(envelope, raw.RoomID != nil); protoErr != nil {
			return envelope, protoErr
		}
		return envelope, validateContent(envelope, raw.Content != nil)
	default:
		return envelope, newRequestError(ErrorCodeUnknownType, fmt.Sprintf("unsupported type %q", envelope.Type), envelope)
	}
}

func validateRoomID(envelope inboundEnvelope, present bool) *ErrorMessage {
	if !present {
		return newRequestError(ErrorCodeMalformedMessage, `field "room_id" is required`, envelope)
	}
	if !roomIDPattern.MatchString(envelope.RoomID) {
		return newRequestError(ErrorCodeInvalidRoomID, "room_id must match "+roomIDPattern.String(), envelope)
	}
	return nil
}

func validateContent(envelope inboundEnvelope, present bool) *ErrorMessage {
	if !present {
		return newRequestError(ErrorCodeMalformedMessage, `field "content" is required`, envelope)
	}
	if utf8.RuneCountInString(envelope.Content) > MaxContentLength {
		return newRequestError(ErrorCodeContentTooLong, fmt.Sprintf("content exceeds %d characters", MaxContentLength), envelope)
	}
	return nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
