package validator

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin/binding"
	playground "github.com/go-playground/validator/v10"
)

// RegisterGinValidators adds this package's rules to Gin's binding
// validator so DTOs can use them as tags, e.g. `binding:"required,username"`.
// Call once at startup, before any request is bound.
//
// Tags:
//   - username: see ValidateUsername
//   - password: see ValidatePassword (strength, at most 72 bytes)
//   - timezone: see ValidateTimezone (an IANA zone name)
//
// It also makes validation errors name fields by their json / form key
// ("full_name") rather than the Go field name ("FullName").
func RegisterGinValidators() error {
	engine, ok := binding.Validator.Engine().(*playground.Validate)
	if !ok {
		return fmt.Errorf("unexpected gin validator engine %T", binding.Validator.Engine())
	}

	engine.RegisterTagNameFunc(fieldName)

	rules := map[string]func(string) error{
		"username": ValidateUsername,
		"password": ValidatePassword,
		"timezone": ValidateTimezone,
	}
	for tag, rule := range rules {
		if err := engine.RegisterValidation(tag, func(fl playground.FieldLevel) bool {
			return rule(fl.Field().String()) == nil
		}); err != nil {
			return fmt.Errorf("register %s validator: %w", tag, err)
		}
	}

	return nil
}

// fieldName is the name a client knows a field by: its json key, else its
// form (query string) key, else the Go name.
func fieldName(field reflect.StructField) string {
	for _, tag := range []string{"json", "form"} {
		name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if name == "-" {
			return ""
		}
		if name != "" {
			return name
		}
	}
	return field.Name
}
