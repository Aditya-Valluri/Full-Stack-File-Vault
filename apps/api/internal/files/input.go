package files

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type validatedFilter struct {
	name, mime string
	min, max   *int64
}

func validateFilter(f Filter) (validatedFilter, error) {
	var result validatedFilter
	if f.NameContains != nil {
		value := *f.NameContains
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 {
			return result, ErrInvalidInput
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return result, ErrInvalidInput
			}
		}
		result.name = value
	}
	if f.MIMEType != nil {
		value := *f.MIMEType
		media, params, err := mime.ParseMediaType(value)
		if err != nil || len(value) > 127 || len(params) > 0 || !strings.Contains(media, "/") || strings.Contains(media, "*") {
			return result, ErrInvalidInput
		}
		result.mime = media
	}
	var err error
	if result.min, err = parseSize(f.MinSizeBytes); err != nil {
		return result, err
	}
	if result.max, err = parseSize(f.MaxSizeBytes); err != nil {
		return result, err
	}
	if result.min != nil && result.max != nil && *result.min > *result.max {
		return result, ErrInvalidInput
	}
	for _, instant := range []*time.Time{f.CreatedFrom, f.CreatedBefore} {
		if instant != nil && (instant.Year() < 1 || instant.Year() > 9999) {
			return result, ErrInvalidInput
		}
	}
	if f.CreatedFrom != nil && f.CreatedBefore != nil && !f.CreatedFrom.Before(*f.CreatedBefore) {
		return result, ErrInvalidInput
	}
	return result, nil
}
func parseSize(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	if len(*value) == 0 || len(*value) > 19 {
		return nil, ErrInvalidInput
	}
	for _, c := range *value {
		if c < '0' || c > '9' {
			return nil, ErrInvalidInput
		}
	}
	n, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return &n, nil
}

type cursor struct {
	Version   int       `json:"v"`
	Owner     string    `json:"owner"`
	CreatedAt time.Time `json:"at"`
	ID        string    `json:"id"`
}

func encodeCursor(c cursor) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}
func decodeCursor(value, owner string) (cursor, error) {
	var c cursor
	if len(value) == 0 || len(value) > 512 {
		return c, ErrInvalidInput
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return c, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil || decoder.Decode(new(any)) != io.EOF {
		return c, ErrInvalidInput
	}
	if c.Version != 1 || c.Owner != owner || !validID(c.ID) || c.CreatedAt.IsZero() || c.CreatedAt.Year() < 1 || c.CreatedAt.Year() > 9999 {
		return c, ErrInvalidInput
	}
	return c, nil
}
func validID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
