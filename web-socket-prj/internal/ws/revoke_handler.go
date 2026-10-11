package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const (
	RevokeRoomPathValue   = "room"
	maxRevokeRequestBytes = 1024
	maxRevokeUserIDLength = 128
)

type MembershipRevoker interface {
	Revoke(userID, roomID string)
}

type revokeRequest struct {
	UserID string `json:"user_id"`
}

func NewRevokeHandler(revoker MembershipRevoker, hub *Hub, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roomID := r.PathValue(RevokeRoomPathValue)
		if !roomIDPattern.MatchString(roomID) {
			http.Error(w, "room must match "+roomIDPattern.String(), http.StatusBadRequest)
			return
		}
		var request revokeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRevokeRequestBytes)).Decode(&request); err != nil {
			http.Error(w, "body must be JSON like {\"user_id\":\"user-a\"}", http.StatusBadRequest)
			return
		}
		if request.UserID == "" || len(request.UserID) > maxRevokeUserIDLength {
			http.Error(w, "user_id is required (max 128 bytes)", http.StatusBadRequest)
			return
		}

		revoker.Revoke(request.UserID, roomID)
		hub.Revoke(request.UserID, roomID)
		logger.Info("membership revoked", "user_id", request.UserID, "room_id", roomID)
		w.WriteHeader(http.StatusNoContent)
	}
}
