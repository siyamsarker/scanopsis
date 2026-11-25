// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxPath = 500
	maxName = 200
	maxKey  = 400
	maxRef  = 255
)

func hasSpaceOrControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0
}

// validatePath checks a GitLab project path: at least "group/project", no
// leading or trailing slash, no empty segment, no whitespace or control
// characters.
func validatePath(p string) error {
	switch {
	case p == "" || utf8.RuneCountInString(p) > maxPath:
		return errors.New("gitlab_project_path must be 1 to 500 characters")
	case hasSpaceOrControl(p):
		return errors.New("gitlab_project_path must not contain spaces")
	case strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		return errors.New("gitlab_project_path must not start or end with a slash")
	case !strings.Contains(p, "/") || strings.Contains(p, "//"):
		return errors.New("gitlab_project_path must look like group/project")
	}
	return nil
}

func validateName(n string) error {
	if strings.TrimSpace(n) == "" || utf8.RuneCountInString(n) > maxName {
		return errors.New("name must be 1 to 200 characters")
	}
	return nil
}

func validateKey(k string) error {
	if k == "" || utf8.RuneCountInString(k) > maxKey || hasSpaceOrControl(k) {
		return errors.New("sonar_project_key must be 1 to 400 characters without spaces")
	}
	return nil
}

// validateCreate returns the name to store (defaulting to the last path
// segment) and the optional SonarQube key.
func validateCreate(path string, name, key *string) (string, *string, error) {
	if err := validatePath(path); err != nil {
		return "", nil, err
	}
	n := path[strings.LastIndex(path, "/")+1:]
	if name != nil {
		if err := validateName(*name); err != nil {
			return "", nil, err
		}
		n = *name
	}
	if key != nil {
		if err := validateKey(*key); err != nil {
			return "", nil, err
		}
	}
	return n, key, nil
}

// validateRef checks a dashboard branch: after trimming, 1 to 255 characters
// without control characters. It returns the value to store.
func validateRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || utf8.RuneCountInString(ref) > maxRef {
		return "", errors.New("default_ref must be 1 to 255 characters")
	}
	if strings.IndexFunc(ref, unicode.IsControl) >= 0 {
		return "", errors.New("default_ref must not contain control characters")
	}
	return ref, nil
}

// validateUpdate checks a service update. ref is the default ref as sent,
// valid only when refSet and not null. cancelDeclared is a
// sonar_key_declared of null.
func validateUpdate(name *string, keySet bool, key *string, archived *bool, refSet bool, ref *string, cancelDeclared bool) error {
	if name == nil && !keySet && archived == nil && !refSet && !cancelDeclared {
		return errors.New("provide at least one of name, sonar_project_key, archived, default_ref and sonar_key_declared")
	}
	if name != nil {
		if err := validateName(*name); err != nil {
			return err
		}
	}
	if keySet && key != nil {
		if err := validateKey(*key); err != nil {
			return err
		}
	}
	if refSet && ref != nil {
		if _, err := validateRef(*ref); err != nil {
			return err
		}
	}
	return nil
}
