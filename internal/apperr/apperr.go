package apperr

import "fmt"

type ValidationError struct {
	Fields map[string]string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.Fields)
}
