package config

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EnvParam is one environment variable the process read at startup, together
// with the effective value it produced.
//
// The console renders these because an environment variable is otherwise
// invisible once the container is running: the only way to answer "why did my
// image request die at exactly 60s" or "is my .env value even in this container"
// was to read the deployment files and reason about compose interpolation. The
// value column is what the process actually uses; FromEnv says whether it came
// from the environment at all — a variable compose never passed looks exactly
// like a deliberate default, and that difference is the whole answer.
type EnvParam struct {
	Key string `json:"key"`
	// Kind is derived from the bound field: bool, int, duration, string, list.
	Kind string `json:"kind"`
	// Value is the effective value, already formatted for display. Secrets are
	// masked here — this payload must never carry a credential.
	Value string `json:"value"`
	// FromEnv is true when the variable was present and non-empty.
	FromEnv bool `json:"from_env"`
	// Secret marks values that are masked.
	Secret bool `json:"secret"`
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	// userinfoPattern finds "scheme://user:password@host" so a credential
	// embedded in a URL is masked while the host stays readable.
	userinfoPattern = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@]*@`)
)

// DeploymentParameters returns every environment variable the process read,
// with the value it is actually running with.
//
// The binding is the env tag on the Config field that holds the value, not a
// table beside it. A variable Load reads without a tag would be invisible in the
// console, and a tag on a field Load never assigns would show the operator a
// plausible number that is not the one in effect; params_test.go checks both
// directions against config.go.
func (c *Config) DeploymentParameters() []EnvParam {
	if c == nil {
		return nil
	}
	value := reflect.ValueOf(c).Elem()
	typ := value.Type()
	params := make([]EnvParam, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := field.Tag.Get("env")
		if key == "" {
			continue
		}
		secret := field.Tag.Get("secret") == "true"
		bound := value.Field(i)
		params = append(params, EnvParam{
			Key:     key,
			Kind:    fieldKind(bound),
			Value:   renderParamValue(bound, secret),
			FromEnv: envParamPresent(key),
			Secret:  secret,
		})
	}
	// The console renders this list in the order it arrives; sorting by key keeps
	// it stable and scannable instead of following the declaration order of a
	// struct that is grouped by area.
	sort.Slice(params, func(i, j int) bool { return params[i].Key < params[j].Key })
	return params
}

// envParamPresent mirrors what the readers treat as "set": a variable that is
// absent, or present but blank, falls back to its default.
func envParamPresent(key string) bool {
	value, ok := os.LookupEnv(key)
	return ok && strings.TrimSpace(value) != ""
}

func fieldKind(field reflect.Value) string {
	switch {
	case field.Type() == durationType:
		return "duration"
	case field.Kind() == reflect.Bool:
		return "bool"
	case field.Kind() == reflect.String:
		return "string"
	case field.Kind() == reflect.Slice:
		return "list"
	default:
		return "int"
	}
}

func renderParamValue(field reflect.Value, secret bool) string {
	switch {
	case field.Type() == durationType:
		return field.Interface().(time.Duration).String()
	case field.Kind() == reflect.String:
		value := field.String()
		if secret {
			return maskParam(value)
		}
		return redactURLUserinfo(value)
	case field.Kind() == reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case field.Kind() == reflect.Slice:
		values := make([]string, 0, field.Len())
		for i := 0; i < field.Len(); i++ {
			values = append(values, field.Index(i).String())
		}
		joined := strings.Join(values, ", ")
		if secret {
			return maskParam(joined)
		}
		return joined
	default:
		return strconv.FormatInt(field.Int(), 10)
	}
}

// maskParam hides a credential without hiding whether one is configured.
func maskParam(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "••••••"
}

// redactURLUserinfo masks user:password inside a URL.
func redactURLUserinfo(value string) string {
	return userinfoPattern.ReplaceAllString(value, "${1}••••••@")
}
