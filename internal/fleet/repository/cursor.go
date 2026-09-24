package repository

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// encodeCursor/decodeCursor implement the base64(created_at|id) cursor
// shape ENGINEERING_STANDARDS.md §4 expects — same pattern as
// internal/ops/repository/cursor.go.
func encodeCursor(createdAt time.Time, id string) string {
	raw := fmt.Sprintf("%s|%s", createdAt.Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (createdAt time.Time, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("fleet: malformed cursor")
	}
	createdAt, err = time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return createdAt, parts[1], nil
}

// encodeSearchCursor/decodeSearchCursor are the same base64(value|id)
// shape, but with an opaque string sort-value (a formatted price) rather
// than a timestamp — used by the price-sorted search query.
func encodeSearchCursor(sortValue, id string) string {
	raw := fmt.Sprintf("%s|%s", sortValue, id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSearchCursor(cursor string) (sortValue float64, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("fleet: malformed cursor")
	}
	sortValue, err = strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, "", err
	}
	return sortValue, parts[1], nil
}
