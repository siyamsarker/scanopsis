// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

// maxPatchBody is the largest accepted PATCH body.
const maxPatchBody = 64 << 10

type settingJSON struct {
	Key         string     `json:"key"`
	Type        Type       `json:"type"`
	Secret      bool       `json:"secret"`
	Source      string     `json:"source"`
	Value       any        `json:"value"`
	IsSet       bool       `json:"is_set"`
	EnvVar      string     `json:"env_var"`
	Description string     `json:"description"`
	UpdatedBy   *string    `json:"updated_by"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

type listJSON struct {
	Items []settingJSON `json:"items"`
}

type errorJSON struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) list() listJSON {
	st := s.cur.Load()
	out := listJSON{Items: make([]settingJSON, 0, len(Definitions))}
	for _, d := range Definitions {
		e := st.entries[d.Key]
		item := settingJSON{
			Key: d.Key, Type: d.Type, Secret: d.Secret, Source: e.source,
			EnvVar: d.EnvVar, Description: d.Description,
		}
		switch v := e.value.(type) {
		case string:
			item.IsSet = v != ""
		case []string:
			item.IsSet = len(v) > 0
		default:
			item.IsSet = true
		}
		if !d.Secret {
			item.Value = e.value
		}
		if e.stored {
			by, at := e.updatedBy, e.updatedAt
			item.UpdatedBy = &by
			if !at.IsZero() {
				item.UpdatedAt = &at
			}
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// ListHandler serves GET /api/v1/settings. The router enforces the admin role.
func (s *Service) ListHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.list())
	})
}

// UpdateHandler serves PATCH /api/v1/settings. The router enforces the
// admin role.
func (s *Service) UpdateHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		invalid := func(msg string) {
			writeJSON(w, http.StatusBadRequest, errorJSON{errorBody{Code: "invalid_request", Message: msg}})
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPatchBody))
		if err != nil {
			invalid("The request body could not be read or is larger than 64 KiB.")
			return
		}
		var patch map[string]any
		if err := json.Unmarshal(body, &patch); err != nil || patch == nil {
			invalid("The body must be a JSON object that maps setting keys to values.")
			return
		}
		actor := "unknown"
		if p, ok := auth.PrincipalFrom(r.Context()); ok {
			actor = p.Source + ":" + p.Username
		}
		fields, err := s.update(r.Context(), patch, actor)
		switch {
		case err != nil:
			s.log.ErrorContext(r.Context(), "cannot save settings", "error", err)
			writeJSON(w, http.StatusInternalServerError, errorJSON{errorBody{Code: "internal_error", Message: "The settings could not be saved."}})
		case len(fields) > 0:
			writeJSON(w, http.StatusBadRequest, errorJSON{errorBody{Code: "invalid_settings", Message: "Some settings are not valid.", Fields: fields}})
		default:
			writeJSON(w, http.StatusOK, s.list())
		}
	})
}
