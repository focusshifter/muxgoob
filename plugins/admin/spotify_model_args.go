package admin

import (
	"fmt"
	"strconv"
	"strings"
)

// An optional numeric target is always the final token, never a model slot.
func parseSpotifyModelArguments(args []string) ([]string, *int64, error) {
	var chatID *int64
	if len(args) > 0 {
		last := args[len(args)-1]
		numeric := strings.TrimPrefix(strings.TrimPrefix(last, "-"), "+")
		if numeric != "" && strings.Trim(numeric, "0123456789") == "" {
			id, err := strconv.ParseInt(last, 10, 64)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid chat_id: %w", err)
			}
			chatID = &id
			args = args[:len(args)-1]
		}
	}
	if len(args) < 1 || len(args) > 3 {
		return nil, nil, fmt.Errorf("specify 1–3 models")
	}
	return args, chatID, nil
}
