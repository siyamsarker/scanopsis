// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"math"
	"strconv"
	"strings"
)

// Condition is one quality gate condition in the form stored and served,
// whether it came from the webhook or the Web API.
type Condition struct {
	Metric    string  `json:"metric"`
	Operator  string  `json:"operator"`
	Threshold string  `json:"threshold"`
	Value     *string `json:"value"`
	Status    string  `json:"status"`
}

// operatorName gives the Web API's short comparators the names the webhook
// uses. Unknown values are kept as reported.
func operatorName(c string) string {
	switch c {
	case "GT":
		return "GREATER_THAN"
	case "LT":
		return "LESS_THAN"
	case "EQ":
		return "EQUALS"
	case "NE":
		return "NOT_EQUALS"
	}
	return c
}

// Modes of the measures.
const (
	modeMQR      = "mqr"
	modeStandard = "standard"
)

// normalised holds the mode-independent values derived from the measures.
// Ratings are "" and the percentages nil when SonarQube did not report them.
type normalised struct {
	Mode            string
	Reliability     string
	Security        string
	Maintainability string
	SecurityReview  string
	Coverage        *float64
	Duplication     *float64
}

// normalise detects the mode from the measures that are present and picks
// the ratings of that mode. It never invents a value.
func normalise(m map[string]string) normalised {
	var n normalised
	has := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := m[k]; ok {
				return true
			}
		}
		return false
	}
	switch {
	case has("software_quality_reliability_rating", "software_quality_security_rating", "software_quality_maintainability_rating"):
		n.Mode = modeMQR
		n.Reliability = rating(m["software_quality_reliability_rating"])
		n.Security = rating(m["software_quality_security_rating"])
		n.Maintainability = rating(m["software_quality_maintainability_rating"])
	case has("reliability_rating", "security_rating", "sqale_rating"):
		n.Mode = modeStandard
		n.Reliability = rating(m["reliability_rating"])
		n.Security = rating(m["security_rating"])
		n.Maintainability = rating(m["sqale_rating"])
	}
	n.SecurityReview = rating(m["security_review_rating"])
	n.Coverage = percent(m, "coverage")
	n.Duplication = percent(m, "duplicated_lines_density")
	return n
}

// rating maps SonarQube's "1.0" to "5.0" to A to E. Anything else, including
// an absent value, gives "".
func rating(v string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f != math.Trunc(f) || f < 1 || f > 5 {
		return ""
	}
	return "ABCDE"[int(f)-1 : int(f)]
}

func percent(m map[string]string, key string) *float64 {
	v, ok := m[key]
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}
