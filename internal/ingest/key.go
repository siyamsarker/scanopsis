// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxArtifactParamLen is the most characters the artifact upload parameter
// may have.
const maxArtifactParamLen = 200

// errArtifactParam is the answer to an artifact upload parameter that cannot
// be a key.
var errArtifactParam = errors.New("artifact must be 1 to 200 printable characters, without control characters")

// checkArtifactParam checks the artifact upload parameter: 1 to 200
// characters of valid UTF-8, each printable (letters, marks, numbers,
// punctuation, symbols and the ASCII space), and not only spaces.
func checkArtifactParam(v string) error {
	if !utf8.ValidString(v) || strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > maxArtifactParamLen ||
		strings.IndexFunc(v, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return errArtifactParam
	}
	return nil
}

// ArtifactKey returns the key that says which scans are of the same artifact:
// the scan of an upload replaces, as the current state, the earlier scans
// with its key, kind and ref, and new and fixed findings are counted against
// them. name is the artifact name of the report, projectPath the GitLab
// project path of the verified token and override the artifact upload
// parameter ("" when not given).
//
// The rules are those migration 00011 used to fill in the keys of older
// scans, and must stay equal to them:
//   - override, when given, is the key; it is checked by checkArtifactParam.
//   - image: the name without its digest (from the first "@") and without its
//     tag (from the first ":" of the last "/"-separated part, so a registry
//     port such as registry:5000/team/app is kept). Letter case is kept.
//   - any other kind (secret-fs): a name that does not start with "/" is its
//     own key, so "." stays "."; an absolute path that ends in
//     "/"+projectPath, the runner's build folder, is "."; an absolute path
//     that contains "/"+projectPath+"/" is the part after its first
//     occurrence without trailing slashes ("." when nothing is left); any
//     other name is its own key. The project path matches in any letter
//     case, as GitLab treats paths; the part kept is in the case of the name.
//   - a name for which these rules leave nothing is its own key.
func ArtifactKey(kind, name, projectPath, override string) (string, error) {
	if override != "" {
		if err := checkArtifactParam(override); err != nil {
			return "", err
		}
		return override, nil
	}
	var key string
	if kind == KindImage {
		key = imageKey(name)
	} else {
		key = folderKey(name, projectPath)
	}
	if key == "" {
		return name, nil
	}
	return key, nil
}

func imageKey(name string) string {
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	last := strings.LastIndexByte(name, '/') + 1
	if i := strings.IndexByte(name[last:], ':'); i >= 0 {
		name = name[:last+i]
	}
	return name
}

func folderKey(name, projectPath string) string {
	if !strings.HasPrefix(name, "/") {
		return name
	}
	folder := "/" + projectPath
	if n := len(name) - len(folder); n >= 0 && strings.EqualFold(name[n:], folder) {
		return "."
	}
	folder += "/"
	if i := indexFold(name, folder); i >= 0 {
		if rest := strings.TrimRight(name[i+len(folder):], "/"); rest != "" {
			return rest
		}
		return "."
	}
	return name
}

// indexFold returns the byte index of the first occurrence of sub, which
// starts with "/", in s, letter case ignored, or -1. Each candidate is
// compared over the same bytes of s that sub has, so the index points into s
// itself: lowering the whole of s first could change its length (İ and Ⱥ
// have longer lower-case forms in UTF-8) and move the part after the match.
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i] == sub[0] && strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// vulnTarget returns the target a vulnerability of a scan is stored with.
// Trivy names the OS packages of an image after the image, tag included
// ("app:3 (alpine 3.20.3)"), and a finding is the same from one scan to the
// next only if its target is, so in an image scan a target that is the
// artifact name, alone or followed by a space, is stored with the name
// replaced by the artifact key ("app (alpine 3.20.3)"). Other targets (the
// files of language packages) are kept. Migration 00011 renames the targets
// of older scans by the same rule.
func vulnTarget(kind, name, key, target string) string {
	if kind != KindImage || name == "" || key == name || !strings.HasPrefix(target, name) ||
		len(target) > len(name) && target[len(name)] != ' ' {
		return target
	}
	return clip(key+target[len(name):], maxFieldLen)
}
