// Package telegram owns Telegram-specific identity and settings policy. It has
// no executor, database, model client, or platform network client.
package telegram

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("telegram: invalid settings confirmation scope")

func Person(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "telegram:user:"))
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return "", ErrInvalid
	}
	return "telegram:user:" + value, nil
}

func Confirmers(members []string) ([]string, error) {
	if len(members) > 128 {
		return nil, ErrInvalid
	}
	result := make([]string, 0, len(members))
	for _, member := range members {
		p, e := Person(member)
		if e != nil {
			return nil, e
		}
		result = append(result, p)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

// OneOption accepts an exact one-field delta of an already selected extension.
// All other capability fields, including explicit exclusions, must survive.
// The host/extension still verifies the candidate and its actual authorization.
func OneOption(before, after json.RawMessage) (extension, key, value string, err error) {
	var a, b map[string]any
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		return "", "", "", ErrInvalid
	}
	var selected []string
	if raw, e := json.Marshal(a["extensions"]); e == nil {
		_ = json.Unmarshal(raw, &selected)
	}
	changes := 0
	for _, field := range []string{"extension_variants", "extension_settings"} {
		x, _ := a[field].(map[string]any)
		y, _ := b[field].(map[string]any)
		ids := map[string]bool{}
		for id := range x {
			ids[id] = true
		}
		for id := range y {
			ids[id] = true
		}
		for id := range ids {
			if reflect.DeepEqual(x[id], y[id]) {
				continue
			}
			if !slices.Contains(selected, id) {
				return "", "", "", ErrInvalid
			}
			if field == "extension_variants" {
				v, ok := y[id].(string)
				if !ok || v == "" {
					return "", "", "", ErrInvalid
				}
				extension, key, value = id, "", v
				changes++
			} else {
				old, _ := x[id].(map[string]any)
				next, _ := y[id].(map[string]any)
				keys := map[string]bool{}
				for k := range old {
					keys[k] = true
				}
				for k := range next {
					keys[k] = true
				}
				for k := range keys {
					if !reflect.DeepEqual(old[k], next[k]) {
						v, ok := next[k].(string)
						if !ok || v == "" {
							return "", "", "", ErrInvalid
						}
						extension, key, value = id, k, v
						changes++
					}
				}
			}
		}
		delete(a, field)
		delete(b, field)
	}
	if changes != 1 || !reflect.DeepEqual(a, b) {
		return "", "", "", ErrInvalid
	}
	return extension, key, value, nil
}
