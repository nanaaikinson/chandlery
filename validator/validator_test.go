package validator

import (
	"reflect"
	"testing"

	z "github.com/Oudwins/zog"
)

func issue(path, message string) *z.ZogIssue {
	return &z.ZogIssue{Path: []string{path}, Message: message}
}

func TestSanitizeIssues(t *testing.T) {
	t.Parallel()

	t.Run("empty input yields no errors", func(t *testing.T) {
		t.Parallel()
		got := SanitizeIssues(nil)
		if len(got) != 0 {
			t.Errorf("SanitizeIssues(nil) = %+v, want empty", got)
		}
	})

	t.Run("one issue per field, in first-seen order", func(t *testing.T) {
		t.Parallel()
		got := SanitizeIssues(z.ZogIssueList{
			issue("email", "required"),
			issue("password", "too short"),
		})
		want := []ValidationError{
			{Field: "email", Reasons: []string{"required"}},
			{Field: "password", Reasons: []string{"too short"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SanitizeIssues() = %+v, want %+v", got, want)
		}
	})

	t.Run("groups multiple reasons for the same field", func(t *testing.T) {
		t.Parallel()
		got := SanitizeIssues(z.ZogIssueList{
			issue("password", "too short"),
			issue("email", "required"),
			issue("password", "must contain a digit"),
		})
		want := []ValidationError{
			{Field: "password", Reasons: []string{"too short", "must contain a digit"}},
			{Field: "email", Reasons: []string{"required"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SanitizeIssues() = %+v, want %+v", got, want)
		}
	})
}
