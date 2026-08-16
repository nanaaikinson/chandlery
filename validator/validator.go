package validator

import z "github.com/Oudwins/zog"

type ValidationError struct {
	Field   string   `json:"field"`
	Reasons []string `json:"reasons"`
}

// SanitizeIssues groups zog's flat issue list into one ValidationError per
// field, collecting every reason that field failed for.
func SanitizeIssues(errs z.ZogIssueList) []ValidationError {
	var result []ValidationError
	byField := make(map[string]int)

	for _, err := range errs {
		field := err.PathString()
		if i, ok := byField[field]; ok {
			result[i].Reasons = append(result[i].Reasons, err.Message)
			continue
		}
		byField[field] = len(result)
		result = append(result, ValidationError{
			Field:   field,
			Reasons: []string{err.Message},
		})
	}
	return result
}
