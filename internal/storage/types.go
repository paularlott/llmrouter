package storage

import (
	"strings"

	"github.com/google/uuid"
)

func GenerateConversationID() string {
	return "conv_" + strings.ReplaceAll(uuid.New().String(), "-", "")
}

func GenerateMessageID() string {
	return "msg_" + strings.ReplaceAll(uuid.New().String(), "-", "")
}
